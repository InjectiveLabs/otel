package otel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	globalotel "go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("APP_OTEL_ADDR", "signoz-k8s-infra-otel-agent.addons:4317")
	t.Setenv("APP_OTEL_PREFIX", "app-api")
	t.Setenv("APP_OTEL_INSECURE", "true")
	t.Setenv("APP_OTEL_TRACING_DISABLED", "true")
	t.Setenv("APP_OTEL_REPORT_CANCELED_AS_ERROR", "true")
	t.Setenv("APP_OTEL_DISABLED", "false")

	cfg, err := ConfigFromEnv("APP")

	require.NoError(t, err)
	require.Equal(t, Config{
		Endpoint:              "signoz-k8s-infra-otel-agent.addons:4317",
		Prefix:                "app-api",
		Insecure:              true,
		TracingDisabled:       true,
		ReportCanceledAsError: true,
		Disabled:              false,
	}, cfg)
}

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg, err := ConfigFromEnv("MISSING")

	require.NoError(t, err)
	require.False(t, cfg.TracingDisabled)
	require.False(t, cfg.ReportCanceledAsError)
	require.True(t, cfg.Disabled)
}

func TestConfigFromEnvWithoutPrefix(t *testing.T) {
	t.Setenv("OTEL_ADDR", "collector:4317")
	t.Setenv("OTEL_PREFIX", "app-api")
	t.Setenv("OTEL_DISABLED", "false")

	cfg, err := ConfigFromEnv("")

	require.NoError(t, err)
	require.Equal(t, "collector:4317", cfg.Endpoint)
	require.Equal(t, "app-api", cfg.Prefix)
	require.False(t, cfg.Disabled)
}

func TestConfigFromEnvAcceptsFullOtelPrefix(t *testing.T) {
	t.Setenv("APP_OTEL_ADDR", "collector:4317")

	cfg, err := ConfigFromEnv("APP_OTEL")

	require.NoError(t, err)
	require.Equal(t, "collector:4317", cfg.Endpoint)
}

func TestConfigFromEnvRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("APP_OTEL_DISABLED", "sometimes")

	_, err := ConfigFromEnv("APP")

	require.ErrorContains(t, err, "APP_OTEL_DISABLED")
}

func TestInitFromEnvDisabledDoesNotInitializeClient(t *testing.T) {
	t.Setenv("APP_OTEL_DISABLED", "true")
	t.Setenv("APP_OTEL_ADDR", "unreachable:4317")
	t.Setenv("APP_OTEL_PREFIX", "app-api")

	shutdown, err := InitFromEnv(context.Background(), "APP")

	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.False(t, metricsEnabled.Load())
	require.NoError(t, shutdown(context.Background()))
}

func TestInitValidatesEnabledConfig(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{Disabled: true})
	require.NoError(t, err)
	require.NoError(t, shutdown(context.Background()))

	_, err = Init(context.Background(), Config{
		Disabled: false,
		Prefix:   "service",
	})
	require.EqualError(t, err, "OpenTelemetry endpoint is required")

	_, err = Init(context.Background(), Config{
		Disabled: false,
		Endpoint: "collector:4317",
	})
	require.EqualError(t, err, "metrics prefix is required")
}

func TestInitDoesNotReplaceGlobalProviders(t *testing.T) {
	globalMeterProvider := sdkmetric.NewMeterProvider()
	globalTracerProvider := sdktrace.NewTracerProvider()
	previousMeterProvider := globalotel.GetMeterProvider()
	previousTracerProvider := globalotel.GetTracerProvider()
	globalotel.SetMeterProvider(globalMeterProvider)
	globalotel.SetTracerProvider(globalTracerProvider)
	t.Cleanup(func() {
		globalotel.SetMeterProvider(previousMeterProvider)
		globalotel.SetTracerProvider(previousTracerProvider)
		require.NoError(t, globalMeterProvider.Shutdown(context.Background()))
		require.NoError(t, globalTracerProvider.Shutdown(context.Background()))
	})

	shutdown, err := Init(context.Background(), Config{
		Endpoint: "127.0.0.1:4317",
		Prefix:   "service",
		Insecure: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		_ = shutdown(shutdownCtx)
	})

	require.Same(t, globalMeterProvider, globalotel.GetMeterProvider())
	require.Same(t, globalTracerProvider, globalotel.GetTracerProvider())
}

func TestInitFromEnvBackgroundRejectsInvalidInterval(t *testing.T) {
	t.Setenv("APP_OTEL_DISABLED", "true")

	shutdown, err := InitFromEnvBackground(context.Background(), "APP", 0)

	require.Nil(t, shutdown)
	require.EqualError(t, err, "OpenTelemetry retry interval must be greater than zero")
}

func TestInitBackgroundRetriesAndRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		attempts     atomic.Int32
		providerStop = make(chan struct{})
	)
	initialized := make(chan struct{})
	initialize := func(context.Context, Config) (Shutdown, error) {
		if attempts.Add(1) < 3 {
			return nil, errors.New("collector unavailable")
		}
		close(initialized)
		return func(context.Context) error {
			close(providerStop)
			return nil
		}, nil
	}

	shutdown, err := initBackground(ctx, Config{}, time.Millisecond, initialize)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	require.Eventually(t, func() bool {
		select {
		case <-initialized:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.Equal(t, int32(3), attempts.Load())

	cancel()
	require.Eventually(t, func() bool {
		select {
		case <-providerStop:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.NoError(t, shutdown(context.Background()))
}

func TestInitBackgroundCanBeStoppedWhileRetrying(t *testing.T) {
	var attempts atomic.Int32
	initialize := func(context.Context, Config) (Shutdown, error) {
		attempts.Add(1)
		return nil, errors.New("collector unavailable")
	}

	shutdown, err := initBackground(
		context.Background(),
		Config{},
		time.Hour,
		initialize,
	)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return attempts.Load() == 1
	}, time.Second, time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, shutdown(shutdownCtx))
	require.Equal(t, int32(1), attempts.Load())
}
