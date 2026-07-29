package otel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestApprovedReportsCompile(t *testing.T) {
	Counter("test.counter", uint64(1), "tag", "value")
	Incr("test.incr", "tag", "value")
	Gauge("test.gauge", 1.5, "tag", "value")
	Histogram("test.histogram", time.Second, "tag", "value")
}

func TestRecordBindsFinalValues(t *testing.T) {
	var err error
	result := "success"
	attempts := 1
	ratio := 1.5
	enabled := false

	rec := Record("test.track", "static", "tag").
		BindErr(&err).
		Bind("result", &result).
		Bind("attempts", &attempts).
		Bind("ratio", &ratio).
		Bind("enabled", &enabled).(*record)

	result = "failure"
	attempts = 2
	ratio = 2.5
	enabled = true
	err = errors.New("boom")

	require.Equal(t, tagSet{
		"static":   "tag",
		"error":    "true",
		"result":   "failure",
		"attempts": "2",
		"ratio":    "2.5",
		"enabled":  "true",
	}, rec.finishTags())
}

func TestRecordBindsNilErrorAsFalse(t *testing.T) {
	var err error

	rec := Record("test.track").BindErr(&err).(*record)

	require.Equal(t, "false", rec.finishTags()["error"])
}

func TestBindErrIgnoresCanceledByDefault(t *testing.T) {
	err := context.Canceled

	rec := Record("test.track").BindErr(&err).(*record)
	event := Event("test.event").BindErr(&err).(*event)

	require.Equal(t, "false", rec.finishTags()["error"])
	require.Equal(t, "false", event.finishTags()["error"])
}

func TestBindErrCanReportCanceledAsError(t *testing.T) {
	reportCanceledAsError.Store(true)
	t.Cleanup(func() {
		reportCanceledAsError.Store(false)
	})
	err := context.Canceled

	rec := Record("test.track").BindErr(&err).(*record)

	require.Equal(t, "true", rec.finishTags()["error"])
}

func TestBindErrReportsDeadlineExceededAsError(t *testing.T) {
	err := context.DeadlineExceeded

	rec := Record("test.track").BindErr(&err).(*record)

	require.Equal(t, "true", rec.finishTags()["error"])
}

func TestRecordDoneIsIdempotent(t *testing.T) {
	rec := Record("test.track")
	rec.Done()
	rec.Done()
	rec.Done()
}

func TestTraceUpdatesContextForFollowingFunction(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	tracingEnabled.Store(true)
	t.Cleanup(func() {
		tracingEnabled.Store(false)
		otel.SetTracerProvider(previousProvider)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	var (
		parentSpanContext trace.SpanContext
		childSpanContext  trace.SpanContext
	)
	func() {
		ctx := context.Background()
		var err error
		defer Trace(&ctx, "parent", "foo", "bar").
			BindErr(&err).
			Done()

		parentSpanContext = trace.SpanContextFromContext(ctx)
		childSpanContext = recordFollowingFunction(ctx)
	}()

	require.True(t, parentSpanContext.IsValid())
	require.True(t, childSpanContext.IsValid())
	require.Equal(t, parentSpanContext.TraceID(), childSpanContext.TraceID())
	require.NotEqual(t, parentSpanContext.SpanID(), childSpanContext.SpanID())

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	childSpan := findSpan(t, spans, "child")
	require.Equal(t, parentSpanContext.SpanID(), childSpan.Parent.SpanID())
}

func recordFollowingFunction(ctx context.Context) trace.SpanContext {
	rec := Record("child").WithSpan(ctx)
	defer rec.Done()
	return trace.SpanContextFromContext(rec.Context())
}

func findSpan(
	t *testing.T,
	spans tracetest.SpanStubs,
	name string,
) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q not found", name)
	return tracetest.SpanStub{}
}

func TestEventResolvesBoundTagsForEveryEmission(t *testing.T) {
	result := "first"
	event := Event("test.event").Bind("result", &result).(*event)

	require.Equal(t, "first", event.finishTags()["result"])
	result = "second"
	require.Equal(t, "second", event.finishTags()["result"])
}

func TestToStringMatchesV2Contract(t *testing.T) {
	stringValue := "value"
	intValue := 42
	boolValue := true
	var nilString *string

	tests := []struct {
		name     string
		value    any
		expected string
		ok       bool
	}{
		{name: "string", value: stringValue, expected: "value", ok: true},
		{name: "string pointer", value: &stringValue, expected: "value", ok: true},
		{name: "int", value: intValue, expected: "42", ok: true},
		{name: "int pointer", value: &intValue, expected: "42", ok: true},
		{name: "bool", value: boolValue, expected: "true", ok: true},
		{name: "bool pointer", value: &boolValue, expected: "true", ok: true},
		{name: "nil pointer", value: nilString, expected: "nil", ok: true},
		{name: "nil", value: nil, expected: "nil", ok: true},
		{name: "unsupported", value: struct{}{}, expected: "", ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, ok := ToString(test.value)
			require.Equal(t, test.ok, ok)
			require.Equal(t, test.expected, actual)
		})
	}
}

func TestMetricsUseGlobalOTelProvider(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previousProvider := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	metricsEnabled.Store(true)
	metricPrefix.Store("")
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(context.Background()))
		otel.SetMeterProvider(previousProvider)
		metricsEnabled.Store(false)
		tracingEnabled.Store(false)
		instruments = instrumentRegistry{
			counters:   make(map[string]otelmetric.Int64Counter),
			gauges:     make(map[string]otelmetric.Float64Gauge),
			histograms: make(map[string]otelmetric.Float64Histogram),
		}
	})

	instruments = instrumentRegistry{
		counters:   make(map[string]otelmetric.Int64Counter),
		gauges:     make(map[string]otelmetric.Float64Gauge),
		histograms: make(map[string]otelmetric.Float64Histogram),
	}

	Counter("orders.processed", uint64(2), "market", "INJ/USDT")
	Gauge("queue.depth", 3, "queue", "orders")
	Histogram("request.latency", 1500*time.Millisecond, "route", "/orders")

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))

	found := make(map[string]metricdata.Metrics)
	for _, scope := range resourceMetrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			found[metric.Name] = metric
		}
	}

	require.Contains(t, found, "orders.processed")
	require.Contains(t, found, "queue.depth")
	require.Contains(t, found, "request.latency")

	sum, ok := found["orders.processed"].Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.True(t, sum.IsMonotonic)
	require.EqualValues(t, 2, sum.DataPoints[0].Value)

	histogram, ok := found["request.latency"].Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.EqualValues(t, 1500, histogram.DataPoints[0].Sum)
}
