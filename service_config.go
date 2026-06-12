package metrics

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ConfigureFromEnv overlays ServiceConfig fields from the default metrics env vars.
// Prefixes "APPNAME" and "APPNAME_METRICS" both map to APPNAME_METRICS_DISABLED.
func (c *ServiceConfig) ConfigureFromEnv(prefix string) error {
	for _, opt := range []struct {
		name  string
		apply func(string) error
	}{
		{"METRICS_DISABLED", setBool(&c.Disabled)},
		{"METRICS_AGENT_ID", setString(&c.AgentID)},
		{"METRICS_AGENT_ADDRESS", setString(&c.AgentAddress)},
		{"METRICS_PREFIX", setString(&c.MetricsPrefix)},
		{"METRICS_ENV_NAME", setString(&c.EnvName)},
		{"METRICS_MOCKING_ENABLED", setBool(&c.MockingEnabled)},
		{"METRICS_MOCKING_THRESHOLD", setDuration(&c.MockingThreshold)},
		{"METRICS_MIXPANEL_ENABLED", setBool(&c.MixPanelEnabled)},
		{"METRICS_MIXPANEL_PROJECT_TOKEN", setString(&c.MixPanelProjectToken)},
		{"METRICS_OTEL_INSECURE", setBool(&c.OTelInsecure)},
		{"METRICS_TRACING_ENABLED", setBool(&c.TracingEnabled)},
	} {
		envName := prefixedEnvName(prefix, opt.name)
		value, ok := os.LookupEnv(envName)
		if !ok {
			continue
		}
		if err := opt.apply(value); err != nil {
			return fmt.Errorf("parse %s: %w", envName, err)
		}
	}

	return nil
}

func (c ServiceConfig) Validate() error {
	if c.Disabled {
		return nil
	}

	var missing []string
	for _, field := range []struct {
		name  string
		value string
	}{
		{"ServiceName", c.ServiceName},
		{"AgentID", c.AgentID},
		{"AgentAddress", c.AgentAddress},
		{"EnvName", c.EnvName},
	} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required ServiceConfig fields: %s", strings.Join(missing, ", "))
	}

	return nil
}

func prefixedEnvName(prefix, name string) string {
	prefix = strings.Trim(prefix, "_")
	if prefix == "" {
		return name
	}
	if strings.HasSuffix(prefix, "_METRICS") {
		if suffix, ok := strings.CutPrefix(name, "METRICS_"); ok {
			return prefix + "_" + suffix
		}
	}
	return prefix + "_" + name
}

func setString(dst *string) func(string) error {
	return func(value string) error {
		*dst = value
		return nil
	}
}

func setBool(dst *bool) func(string) error {
	return func(value string) error {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		*dst = parsed
		return nil
	}
}

func setDuration(dst *time.Duration) func(string) error {
	return func(value string) error {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*dst = parsed
		return nil
	}
}
