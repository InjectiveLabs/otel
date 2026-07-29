# Metrics v2

`v2` exposes the preferred client-facing metrics API. It keeps the surface small and nudges callers toward monotonic counters, explicit duration recording, and low-noise tags.

Import it as `metrics`:

```go
import metrics "github.com/InjectiveLabs/metrics/v2"
```

Importing `v2` also enables two process-wide defaults:

- OpenTelemetry `Counter` and `Incr` use monotonic counters.
- stuck-function reporting is disabled.

## Counters

Use `Counter` for unsigned count values and `Incr` for incrementing by one.

```go
metrics.Counter("orders.processed", uint64(count), "market", marketID)
metrics.Incr("orders.accepted", "market", marketID)
```

`Counter` only accepts unsigned values, so negative counter values are rejected at compile time.

## Gauges

Use `Gauge` for values that can move up and down.

```go
metrics.Gauge("queue.depth", float64(depth), "queue", queueName)
```

## Histograms

Use `Histogram` for duration distributions. Durations are emitted in milliseconds.

```go
metrics.Histogram("request.latency", time.Since(start), "route", routeName)
```

## Events

Use `Event` when you want to bind tags once and decide which metric to emit later. This is useful in deferred code paths where the final tags are only known at return time.

```go
func ProcessOrder(ctx context.Context, marketID string) (result string, err error) {
	defer metrics.Event("order.processed", "market", marketID).
		BindErr(&err).
		Bind("result", &result).
		Incr()

	result = "accepted"
	return result, nil
}
```

`Event` can emit any single metric operation, and repeated terminal calls emit repeated metrics.

```go
event := metrics.Event("order.queue", "market", marketID)
event.Gauge(float64(depth), "side", "buy")
event.Counter(uint64(processed), "side", "buy")
```

## Duration Recording

Use `Record` for scoped duration recording. `Done` emits the elapsed duration as a histogram.

```go
func ProcessOrder(ctx context.Context, marketID string) (err error) {
	defer metrics.Record("order.process").
		BindErr(&err).
		Done("market", marketID)

	return process(ctx, marketID)
}
```

`Done` is safe to call more than once. Only the first call records a metric.

## Bound Tags

Use `Bind` when a tag value is only known at the end of the function. Pointer values are resolved when `Done` runs.

```go
func Match(ctx context.Context) (err error) {
	result := "unknown"

	defer metrics.Record("match.run").
		BindErr(&err).
		Bind("result", &result).
		Done()

	result = "success"
	return nil
}
```

Nil scalar pointers are recorded as `"nil"`.

## Spans

Use `WithSpan` when the duration record should also create a tracing span.

```go
func Handle(ctx context.Context) (err error) {
	rec := metrics.Record("handler.run").
		WithSpan(ctx).
		BindErr(&err)

	ctx = rec.Context()
	defer rec.Done()

	return handle(ctx)
}
```

If tracing is not configured, `WithSpan` is a no-op and `Context` returns the original context.
