package otel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var (
	initMu      sync.Mutex
	initialized bool
)

var ErrAlreadyInitialized = errors.New("OpenTelemetry metrics already initialized")

const backgroundShutdownTimeout = 10 * time.Second

// Config controls the OTLP metrics and tracing exporters.
type Config struct {
	Endpoint              string
	Prefix                string
	Insecure              bool
	TracingEnabled        bool
	ReportCanceledAsError bool
	Disabled              bool
}

// Shutdown stops the exporters and flushes pending telemetry.
type Shutdown func(context.Context) error

// ConfigFromEnv reads the existing <PREFIX>_STATSD_* environment variables.
// Tracing is enabled by default when STATSD_TRACING_ENABLED is unset. Metrics
// are disabled by default when STATSD_DISABLED is unset.
func ConfigFromEnv(prefix string) (Config, error) {
	cfg := Config{
		TracingEnabled: true,
		Disabled:       true,
	}

	var err error
	cfg.Endpoint = os.Getenv(envName(prefix, "ADDR"))
	cfg.Prefix = os.Getenv(envName(prefix, "PREFIX"))
	if cfg.Insecure, err = boolFromEnv(prefix, "OTEL_INSECURE", false); err != nil {
		return Config{}, err
	}
	if cfg.TracingEnabled, err = boolFromEnv(prefix, "TRACING_ENABLED", true); err != nil {
		return Config{}, err
	}
	if cfg.ReportCanceledAsError, err = boolFromEnv(prefix, "REPORT_CANCELED_AS_ERROR", false); err != nil {
		return Config{}, err
	}
	if cfg.Disabled, err = boolFromEnv(prefix, "DISABLED", true); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// InitFromEnv initializes OpenTelemetry using <PREFIX>_STATSD_* variables.
func InitFromEnv(ctx context.Context, prefix string) (Shutdown, error) {
	cfg, err := ConfigFromEnv(prefix)
	if err != nil {
		return nil, err
	}
	return Init(ctx, cfg)
}

// InitFromEnvBackground initializes OpenTelemetry in the background using
// <PREFIX>_STATSD_* variables. It returns immediately and retries failed
// initialization attempts until ctx is canceled or shutdown is called.
func InitFromEnvBackground(
	ctx context.Context,
	prefix string,
	retryInterval time.Duration,
) (Shutdown, error) {
	cfg, err := ConfigFromEnv(prefix)
	if err != nil {
		return nil, err
	}
	return InitBackground(ctx, cfg, retryInterval)
}

// InitBackground initializes OpenTelemetry in the background. Initialization
// errors are passed to the global OpenTelemetry error handler and retried after
// retryInterval. Canceling ctx also shuts down a successfully initialized
// provider.
func InitBackground(
	ctx context.Context,
	cfg Config,
	retryInterval time.Duration,
) (Shutdown, error) {
	return initBackground(ctx, cfg, retryInterval, Init)
}

func initBackground(
	ctx context.Context,
	cfg Config,
	retryInterval time.Duration,
	initialize func(context.Context, Config) (Shutdown, error),
) (Shutdown, error) {
	if retryInterval <= 0 {
		return nil, errors.New("OpenTelemetry retry interval must be greater than zero")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runCtx, cancel := context.WithCancel(ctx)
	var (
		worker           sync.WaitGroup
		providerShutdown Shutdown
	)
	worker.Add(1)
	go func() {
		defer worker.Done()

		for {
			shutdown, err := initialize(runCtx, cfg)
			if err == nil {
				providerShutdown = shutdown
				<-runCtx.Done()
				return
			}

			otel.Handle(fmt.Errorf("initialize OpenTelemetry: %w", err))

			timer := time.NewTimer(retryInterval)
			select {
			case <-runCtx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
		}
	}()

	var (
		shutdownOnce sync.Once
		shutdownErr  error
		stopped      = make(chan struct{})
	)
	shutdown := func(shutdownCtx context.Context) error {
		shutdownOnce.Do(func() {
			cancel()
			worker.Wait()
			if providerShutdown != nil {
				shutdownErr = providerShutdown(shutdownCtx)
			}
			close(stopped)
		})
		return shutdownErr
	}

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(
				context.Background(),
				backgroundShutdownTimeout,
			)
			defer shutdownCancel()
			_ = shutdown(shutdownCtx)
		case <-stopped:
		}
	}()

	return shutdown, nil
}

// Init configures the global OpenTelemetry providers. When Disabled is true,
// no exporter or client connection is created and metric operations are no-op.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Prefix = strings.Trim(strings.TrimSpace(cfg.Prefix), ".")
	reportCanceledAsError.Store(cfg.ReportCanceledAsError)

	if cfg.Disabled {
		metricsEnabled.Store(false)
		tracingEnabled.Store(false)
		return noopShutdown, nil
	}
	if cfg.Endpoint == "" {
		return nil, errors.New("OpenTelemetry endpoint is required")
	}
	if cfg.Prefix == "" {
		return nil, errors.New("metrics prefix is required")
	}

	initMu.Lock()
	defer initMu.Unlock()
	if initialized {
		return nil, ErrAlreadyInitialized
	}

	res, err := resource.New(
		ctx,
		resource.WithAttributes(attribute.String("service.name", cfg.Prefix)),
	)
	if err != nil {
		return nil, fmt.Errorf("create OpenTelemetry resource: %w", err)
	}

	metricOptions := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		metricOptions = append(metricOptions, otlpmetricgrpc.WithInsecure())
	}
	metricExporter, err := otlpmetricgrpc.New(ctx, metricOptions...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)

	var tracerProvider *sdktrace.TracerProvider
	if cfg.TracingEnabled {
		traceOptions := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
		}
		traceExporter, traceErr := otlptracegrpc.New(ctx, traceOptions...)
		if traceErr != nil {
			_ = meterProvider.Shutdown(ctx)
			return nil, fmt.Errorf("create OTLP trace exporter: %w", traceErr)
		}
		tracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(traceExporter),
		)
	}

	otel.SetMeterProvider(meterProvider)
	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	metricPrefix.Store(cfg.Prefix)
	instruments.reset()
	metricsEnabled.Store(true)
	tracingEnabled.Store(tracerProvider != nil)
	initialized = true

	var (
		once        sync.Once
		shutdownErr error
	)
	return func(ctx context.Context) error {
		once.Do(func() {
			if ctx == nil {
				ctx = context.Background()
			}
			metricsEnabled.Store(false)
			tracingEnabled.Store(false)

			var traceErr error
			if tracerProvider != nil {
				traceErr = tracerProvider.Shutdown(ctx)
			}
			shutdownErr = errors.Join(
				meterProvider.Shutdown(ctx),
				traceErr,
			)

			initMu.Lock()
			initialized = false
			initMu.Unlock()
		})
		return shutdownErr
	}, nil
}

func boolFromEnv(prefix, suffix string, fallback bool) (bool, error) {
	name := envName(prefix, suffix)
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func envName(prefix, suffix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		return "STATSD_" + suffix
	}
	if strings.HasSuffix(prefix, "_STATSD") {
		return prefix + "_" + suffix
	}
	return prefix + "_STATSD_" + suffix
}

func noopShutdown(context.Context) error {
	return nil
}
