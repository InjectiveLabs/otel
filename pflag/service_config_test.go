package pflag

import (
	"context"
	"testing"
	"time"

	"github.com/InjectiveLabs/metrics"
	spf13pflag "github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestConfigureServiceConfigFromFlagSetWithChangedFlags(t *testing.T) {
	flags := spf13pflag.NewFlagSet("test", spf13pflag.ContinueOnError)
	RegisterServiceConfigFlags(flags)
	require.NoError(t, flags.Set(serviceConfigDisabledFlag, "false"))
	require.NoError(t, flags.Set(serviceConfigAgentIDFlag, "app-local"))
	require.NoError(t, flags.Set(serviceConfigAgentAddressFlag, "localhost:8125"))
	require.NoError(t, flags.Set(serviceConfigPrefixFlag, "app.local"))
	require.NoError(t, flags.Set(serviceConfigEnvNameFlag, "local"))
	require.NoError(t, flags.Set(serviceConfigMockingEnabledFlag, "true"))
	require.NoError(t, flags.Set(serviceConfigMockingThresholdFlag, "50ms"))
	require.NoError(t, flags.Set(serviceConfigMixPanelEnabledFlag, "true"))
	require.NoError(t, flags.Set(serviceConfigMixPanelProjectTokenFlag, "project-token"))
	require.NoError(t, flags.Set(serviceConfigOTELInsecureFlag, "true"))
	require.NoError(t, flags.Set(serviceConfigTracingEnabledFlag, "true"))

	cfg := metrics.ServiceConfig{
		ServiceName:     "app",
		OTelUseCounters: true,
	}

	require.NoError(t, ConfigureServiceConfigFromFlagSet(&cfg, flags))
	require.Equal(t, metrics.ServiceConfig{
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

func TestConfigureServiceConfigFromFlagSetKeepsExistingValuesWhenUnchanged(t *testing.T) {
	flags := spf13pflag.NewFlagSet("test", spf13pflag.ContinueOnError)
	RegisterServiceConfigFlags(flags)

	cfg := metrics.ServiceConfig{
		AgentID:          metrics.TelegrafAgent,
		AgentAddress:     "localhost:8125",
		MetricsPrefix:    "default",
		EnvName:          "local",
		MockingThreshold: time.Second,
	}

	require.NoError(t, ConfigureServiceConfigFromFlagSet(&cfg, flags))
	require.False(t, cfg.Disabled)
	require.Equal(t, metrics.TelegrafAgent, cfg.AgentID)
	require.Equal(t, "localhost:8125", cfg.AgentAddress)
	require.Equal(t, "default", cfg.MetricsPrefix)
	require.Equal(t, "local", cfg.EnvName)
	require.Equal(t, time.Second, cfg.MockingThreshold)
}

func TestConfigureServiceConfigFromFlagSetAllowsExplicitDefaultValue(t *testing.T) {
	flags := spf13pflag.NewFlagSet("test", spf13pflag.ContinueOnError)
	RegisterServiceConfigFlags(flags)
	require.NoError(t, flags.Set(serviceConfigDisabledFlag, "true"))

	cfg := metrics.ServiceConfig{Disabled: false}
	require.NoError(t, ConfigureServiceConfigFromFlagSet(&cfg, flags))
	require.True(t, cfg.Disabled)
}

func TestConfigureServiceConfigFromFlagSetSkipsNilFlagSet(t *testing.T) {
	cfg := metrics.ServiceConfig{AgentID: metrics.TelegrafAgent}
	require.NoError(t, ConfigureServiceConfigFromFlagSet(&cfg, nil))
	require.Equal(t, metrics.TelegrafAgent, cfg.AgentID)
}

func TestConfigureServiceConfigFromFlagSetSkipsNilConfig(t *testing.T) {
	flags := spf13pflag.NewFlagSet("test", spf13pflag.ContinueOnError)
	RegisterServiceConfigFlags(flags)
	require.NoError(t, ConfigureServiceConfigFromFlagSet(nil, flags))
}

func TestConfigureServiceConfigLoadsEnvAndFlagOverrides(t *testing.T) {
	t.Setenv("APPNAME_METRICS_DISABLED", "false")
	t.Setenv("APPNAME_METRICS_AGENT_ID", "env-agent")
	t.Setenv("APPNAME_METRICS_AGENT_ADDRESS", "env-host:8125")
	t.Setenv("APPNAME_METRICS_PREFIX", "env.prefix")
	t.Setenv("APPNAME_METRICS_ENV_NAME", "local")

	flags := spf13pflag.NewFlagSet("test", spf13pflag.ContinueOnError)
	RegisterServiceConfigFlags(flags)
	require.NoError(t, flags.Set(serviceConfigAgentIDFlag, "flag-agent"))

	cfg, err := ConfigureServiceConfig("app", "APPNAME", flags)
	require.NoError(t, err)
	require.Equal(t, "app", cfg.ServiceName)
	require.False(t, cfg.Disabled)
	require.Equal(t, "flag-agent", cfg.AgentID)
	require.Equal(t, "env-host:8125", cfg.AgentAddress)
	require.Equal(t, "env.prefix", cfg.MetricsPrefix)
	require.Equal(t, "local", cfg.EnvName)
}

func TestConfigureServiceConfigWrapsEnvErrors(t *testing.T) {
	t.Setenv("APPNAME_METRICS_MOCKING_THRESHOLD", "eventually")

	_, err := ConfigureServiceConfig("app", "APPNAME", nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "configure service config from env")
	require.ErrorContains(t, err, "APPNAME_METRICS_MOCKING_THRESHOLD")
}

func TestInitServiceWithDisabledConfig(t *testing.T) {
	t.Setenv("APPNAME_METRICS_DISABLED", "true")

	closeFn, err := InitService(context.Background(), "app", "APPNAME", nil)
	require.NoError(t, err)
	require.NotNil(t, closeFn)
	closeFn(time.Second)
}
