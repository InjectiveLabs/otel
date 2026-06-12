package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceConfigConfigureFromEnvWithPrefix(t *testing.T) {
	t.Setenv("APPNAME_METRICS_DISABLED", "false")
	t.Setenv("APPNAME_METRICS_AGENT_ID", "app-local")
	t.Setenv("APPNAME_METRICS_AGENT_ADDRESS", "localhost:8125")
	t.Setenv("APPNAME_METRICS_PREFIX", "app.local")
	t.Setenv("APPNAME_METRICS_ENV_NAME", "local")
	t.Setenv("APPNAME_METRICS_MOCKING_ENABLED", "true")
	t.Setenv("APPNAME_METRICS_MOCKING_THRESHOLD", "50ms")
	t.Setenv("APPNAME_METRICS_MIXPANEL_ENABLED", "true")
	t.Setenv("APPNAME_METRICS_MIXPANEL_PROJECT_TOKEN", "project-token")
	t.Setenv("APPNAME_METRICS_OTEL_INSECURE", "true")
	t.Setenv("APPNAME_METRICS_TRACING_ENABLED", "true")

	cfg := ServiceConfig{
		ServiceName:     "app",
		OTelUseCounters: true,
	}

	require.NoError(t, cfg.ConfigureFromEnv("APPNAME"))
	require.Equal(t, ServiceConfig{
		Disabled:             false,
		ServiceName:          "app",
		AgentID:              "app-local",
		AgentAddress:         "localhost:8125",
		MetricsPrefix:        "app.local",
		EnvName:              "local",
		MockingEnabled:       true,
		MockingThreshold:     50 * time.Millisecond,
		MixPanelEnabled:      true,
		MixPanelProjectToken: "project-token",
		OTelInsecure:         true,
		OTelUseCounters:      true,
		TracingEnabled:       true,
	}, cfg)
}

func TestServiceConfigConfigureFromEnvKeepsExistingValuesWhenUnset(t *testing.T) {
	cfg := ServiceConfig{
		AgentID:          TelegrafAgent,
		AgentAddress:     "localhost:8125",
		MetricsPrefix:    "default",
		MockingThreshold: time.Second,
	}

	require.NoError(t, cfg.ConfigureFromEnv("MISSING"))
	require.Equal(t, TelegrafAgent, cfg.AgentID)
	require.Equal(t, "localhost:8125", cfg.AgentAddress)
	require.Equal(t, "default", cfg.MetricsPrefix)
	require.Equal(t, time.Second, cfg.MockingThreshold)
}

func TestServiceConfigConfigureFromEnvReportsInvalidValues(t *testing.T) {
	t.Setenv("APPNAME_METRICS_MOCKING_THRESHOLD", "eventually")

	var cfg ServiceConfig
	err := cfg.ConfigureFromEnv("APPNAME")
	require.Error(t, err)
	require.ErrorContains(t, err, "APPNAME_METRICS_MOCKING_THRESHOLD")
}

func TestServiceConfigConfigureFromEnvWithoutPrefix(t *testing.T) {
	t.Setenv("METRICS_AGENT_ID", OTELAgent)

	var cfg ServiceConfig
	require.NoError(t, cfg.ConfigureFromEnv(""))
	require.Equal(t, OTELAgent, cfg.AgentID)
}

func TestServiceConfigConfigureFromEnvWithFullMetricsPrefix(t *testing.T) {
	t.Setenv("APPNAME_METRICS_DISABLED", "true")

	var cfg ServiceConfig
	require.NoError(t, cfg.ConfigureFromEnv("APPNAME_METRICS"))
	require.True(t, cfg.Disabled)
}

func TestServiceConfigValidateRequiresFields(t *testing.T) {
	err := ServiceConfig{}.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "ServiceName")
	require.ErrorContains(t, err, "AgentID")
	require.ErrorContains(t, err, "AgentAddress")
	require.ErrorContains(t, err, "EnvName")
}

func TestServiceConfigValidateSkipsRequiredFieldsWhenDisabled(t *testing.T) {
	require.NoError(t, ServiceConfig{Disabled: true}.Validate())
}

func TestServiceConfigValidateAcceptsRequiredFields(t *testing.T) {
	cfg := ServiceConfig{
		ServiceName:  "app",
		AgentID:      TelegrafAgent,
		AgentAddress: "localhost:8125",
		EnvName:      "local",
	}

	require.NoError(t, cfg.Validate())
}

func TestServiceConfigValidateTrimsRequiredFields(t *testing.T) {
	cfg := ServiceConfig{
		ServiceName:  " ",
		AgentID:      TelegrafAgent,
		AgentAddress: "localhost:8125",
		EnvName:      "local",
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "ServiceName")
}
