package otel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("INDEXER_STATSD_ADDR", "signoz-k8s-infra-otel-agent.addons:4317")
	t.Setenv("INDEXER_STATSD_PREFIX", "indexer-rfq-api")
	t.Setenv("INDEXER_STATSD_OTEL_INSECURE", "true")
	t.Setenv("INDEXER_STATSD_TRACING_ENABLED", "false")
	t.Setenv("INDEXER_STATSD_REPORT_CANCELED_AS_ERROR", "true")
	t.Setenv("INDEXER_STATSD_DISABLED", "false")
	t.Setenv("INDEXER_STATSD_OTEL_USE_COUNTERS", "false")
	t.Setenv("INDEXER_STATSD_MOCKING", "true")

	cfg, err := ConfigFromEnv("INDEXER")

	require.NoError(t, err)
	require.Equal(t, Config{
		Endpoint:              "signoz-k8s-infra-otel-agent.addons:4317",
		Prefix:                "indexer-rfq-api",
		Insecure:              true,
		TracingEnabled:        false,
		ReportCanceledAsError: true,
		Disabled:              false,
	}, cfg)
}

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg, err := ConfigFromEnv("MISSING")

	require.NoError(t, err)
	require.True(t, cfg.TracingEnabled)
	require.False(t, cfg.ReportCanceledAsError)
	require.True(t, cfg.Disabled)
}

func TestConfigFromEnvAcceptsFullStatsdPrefix(t *testing.T) {
	t.Setenv("INDEXER_STATSD_ADDR", "collector:4317")
	t.Setenv("INDEXER_STATSD_PREFIX", "indexer-api")
	t.Setenv("INDEXER_STATSD_DISABLED", "false")

	cfg, err := ConfigFromEnv("INDEXER_STATSD")

	require.NoError(t, err)
	require.Equal(t, "collector:4317", cfg.Endpoint)
	require.Equal(t, "indexer-api", cfg.Prefix)
	require.False(t, cfg.Disabled)
}

func TestConfigFromEnvRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("INDEXER_STATSD_DISABLED", "sometimes")

	_, err := ConfigFromEnv("INDEXER")

	require.ErrorContains(t, err, "INDEXER_STATSD_DISABLED")
}

func TestInitFromEnvDisabledDoesNotInitializeClient(t *testing.T) {
	t.Setenv("INDEXER_STATSD_DISABLED", "true")
	t.Setenv("INDEXER_STATSD_ADDR", "unreachable:4317")
	t.Setenv("INDEXER_STATSD_PREFIX", "indexer-rfq-api")

	shutdown, err := InitFromEnv(context.Background(), "INDEXER")

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

func TestInitFromEnvBackgroundRejectsInvalidInterval(t *testing.T) {
	t.Setenv("INDEXER_STATSD_DISABLED", "true")

	shutdown, err := InitFromEnvBackground(context.Background(), "INDEXER", 0)

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
