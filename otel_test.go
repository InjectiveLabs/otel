package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func newTestOTELStatter(useCounters bool) (*otelStatter, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return &otelStatter{
		meter:          mp.Meter("test"),
		meterProvider:  mp,
		useCounters:    useCounters,
		counters:       make(map[string]otelmetric.Int64Counter),
		updownCounters: make(map[string]otelmetric.Int64UpDownCounter),
		gauges:         make(map[string]otelmetric.Float64Gauge),
		histograms:     make(map[string]otelmetric.Float64Histogram),
	}, reader
}

func collectOTELMetric(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Metrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	require.Failf(t, "metric not found", "metric %q not found", name)
	return metricdata.Metrics{}
}

func requireOTELInt64Sum(t *testing.T, m metricdata.Metrics) metricdata.Sum[int64] {
	t.Helper()

	sum, ok := m.Data.(metricdata.Sum[int64])
	require.Truef(t, ok, "expected int64 sum, got %T", m.Data)
	return sum
}

func TestOTELCountDefaultsToUpDownCounter(t *testing.T) {
	statter, reader := newTestOTELStatter(false)
	t.Cleanup(func() {
		require.NoError(t, statter.Close())
	})

	require.NoError(t, statter.Count("events.total", 5, []string{"status=ok"}, 1))

	sum := requireOTELInt64Sum(t, collectOTELMetric(t, reader, "events.total"))
	require.False(t, sum.IsMonotonic)
	require.Len(t, sum.DataPoints, 1)
	require.EqualValues(t, 5, sum.DataPoints[0].Value)
}

func TestOTELCountCanUseCounter(t *testing.T) {
	statter, reader := newTestOTELStatter(true)
	t.Cleanup(func() {
		require.NoError(t, statter.Close())
	})

	require.NoError(t, statter.Count("events.total", 5, []string{"status=ok"}, 1))
	require.NoError(t, statter.Incr("events.total", []string{"status=ok"}, 1))

	sum := requireOTELInt64Sum(t, collectOTELMetric(t, reader, "events.total"))
	require.True(t, sum.IsMonotonic)
	require.Len(t, sum.DataPoints, 1)
	require.EqualValues(t, 6, sum.DataPoints[0].Value)
}

func TestOTELDecrUsesUpDownCounterWithCounterOption(t *testing.T) {
	statter, reader := newTestOTELStatter(true)
	t.Cleanup(func() {
		require.NoError(t, statter.Close())
	})

	require.NoError(t, statter.Decr("active.requests", []string{"status=ok"}, 1))

	sum := requireOTELInt64Sum(t, collectOTELMetric(t, reader, "active.requests"))
	require.False(t, sum.IsMonotonic)
	require.Len(t, sum.DataPoints, 1)
	require.EqualValues(t, -1, sum.DataPoints[0].Value)
}
