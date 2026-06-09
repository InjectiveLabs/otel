package pflag

import (
	"context"
	"fmt"
	"time"

	"github.com/InjectiveLabs/metrics"
	spf13pflag "github.com/spf13/pflag"
)

const (
	serviceConfigDisabledFlag             = "metrics-disabled"
	serviceConfigAgentIDFlag              = "metrics-agent-id"
	serviceConfigAgentAddressFlag         = "metrics-agent-address"
	serviceConfigPrefixFlag               = "metrics-prefix"
	serviceConfigEnvNameFlag              = "metrics-env-name"
	serviceConfigMockingEnabledFlag       = "metrics-mocking-enabled"
	serviceConfigMockingThresholdFlag     = "metrics-mocking-threshold"
	serviceConfigMixPanelEnabledFlag      = "metrics-mixpanel-enabled"
	serviceConfigMixPanelProjectTokenFlag = "metrics-mixpanel-project-token"
	serviceConfigOTELInsecureFlag         = "metrics-otel-insecure"
	serviceConfigTracingEnabledFlag       = "metrics-tracing-enabled"
)

func RegisterServiceConfigFlags(flags *spf13pflag.FlagSet) {
	if flags == nil {
		return
	}

	flags.Bool(serviceConfigDisabledFlag, true, "disable metrics collection")
	flags.String(serviceConfigAgentIDFlag, "", "metrics agent identifier")
	flags.String(serviceConfigAgentAddressFlag, "", "metrics agent address")
	flags.String(serviceConfigPrefixFlag, "", "metrics name prefix")
	flags.String(serviceConfigEnvNameFlag, "", "metrics environment name")
	flags.Bool(serviceConfigMockingEnabledFlag, false, "enable metrics mocking")
	flags.Duration(serviceConfigMockingThresholdFlag, 0, "metrics mocking threshold")
	flags.Bool(serviceConfigMixPanelEnabledFlag, false, "enable Mixpanel metrics")
	flags.String(serviceConfigMixPanelProjectTokenFlag, "", "Mixpanel project token")
	flags.Bool(serviceConfigOTELInsecureFlag, false, "use insecure OpenTelemetry transport")
	flags.Bool(serviceConfigTracingEnabledFlag, false, "enable metrics tracing")
}

func ConfigureServiceConfig(serviceName, envPrefix string, flags *spf13pflag.FlagSet) (metrics.ServiceConfig, error) {
	cfg := metrics.ServiceConfig{
		ServiceName: serviceName,
	}
	if err := cfg.ConfigureFromEnv(envPrefix); err != nil {
		return cfg, fmt.Errorf("configure service config from env: %w", err)
	}
	if err := ConfigureServiceConfigFromFlagSet(&cfg, flags); err != nil {
		return cfg, fmt.Errorf("configure service config from flags: %w", err)
	}

	return cfg, nil
}

func InitService(ctx context.Context, serviceName, envPrefix string, flags *spf13pflag.FlagSet) (func(timeout time.Duration), error) {
	closeFn := func(time.Duration) {}

	cfg, err := ConfigureServiceConfig(serviceName, envPrefix, flags)
	if err != nil {
		return closeFn, err
	}

	return metrics.InitService(ctx, cfg)
}

// ConfigureServiceConfigFromFlagSet overlays ServiceConfig fields from explicitly changed flags.
func ConfigureServiceConfigFromFlagSet(cfg *metrics.ServiceConfig, flags *spf13pflag.FlagSet) error {
	if cfg == nil {
		return nil
	}

	for _, opt := range []struct {
		name  string
		apply func(*spf13pflag.FlagSet, string) error
	}{
		{serviceConfigDisabledFlag, getBoolFlag(&cfg.Disabled)},
		{serviceConfigAgentIDFlag, getStringFlag(&cfg.AgentID)},
		{serviceConfigAgentAddressFlag, getStringFlag(&cfg.AgentAddress)},
		{serviceConfigPrefixFlag, getStringFlag(&cfg.MetricsPrefix)},
		{serviceConfigEnvNameFlag, getStringFlag(&cfg.EnvName)},
		{serviceConfigMockingEnabledFlag, getBoolFlag(&cfg.MockingEnabled)},
		{serviceConfigMockingThresholdFlag, getDurationFlag(&cfg.MockingThreshold)},
		{serviceConfigMixPanelEnabledFlag, getBoolFlag(&cfg.MixPanelEnabled)},
		{serviceConfigMixPanelProjectTokenFlag, getStringFlag(&cfg.MixPanelProjectToken)},
		{serviceConfigOTELInsecureFlag, getBoolFlag(&cfg.OTelInsecure)},
		{serviceConfigTracingEnabledFlag, getBoolFlag(&cfg.TracingEnabled)},
	} {
		if flags == nil || !flags.Changed(opt.name) {
			continue
		}
		if err := opt.apply(flags, opt.name); err != nil {
			return fmt.Errorf("parse flag %s: %w", opt.name, err)
		}
	}

	return nil
}

func getStringFlag(dst *string) func(*spf13pflag.FlagSet, string) error {
	return func(flags *spf13pflag.FlagSet, name string) error {
		parsed, err := flags.GetString(name)
		if err != nil {
			return err
		}
		*dst = parsed
		return nil
	}
}

func getBoolFlag(dst *bool) func(*spf13pflag.FlagSet, string) error {
	return func(flags *spf13pflag.FlagSet, name string) error {
		parsed, err := flags.GetBool(name)
		if err != nil {
			return err
		}
		*dst = parsed
		return nil
	}
}

func getDurationFlag(dst *time.Duration) func(*spf13pflag.FlagSet, string) error {
	return func(flags *spf13pflag.FlagSet, name string) error {
		parsed, err := flags.GetDuration(name)
		if err != nil {
			return err
		}
		*dst = parsed
		return nil
	}
}
