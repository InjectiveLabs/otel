# OpenTelemetry metrics

This module exposes the approved metrics API backed exclusively by
[OpenTelemetry](https://opentelemetry.io/).

The package name remains `metrics` so call sites stay concise:

```go
import metrics "github.com/InjectiveLabs/otel"
```

## Initialization

Initialize the OTLP exporters from the environment:

```go
shutdown, err := metrics.InitFromEnv(ctx, "INDEXER")
if err != nil {
	return err
}
defer shutdown(context.Background())
```

To avoid blocking application startup while the collector is unavailable,
initialize in the background and choose the retry interval:

```go
shutdown, err := metrics.InitFromEnvBackground(ctx, "INDEXER", 30*time.Second)
if err != nil {
	return err
}
defer shutdown(context.Background())
```

This call returns before connecting. Initialization failures are sent to the
global OpenTelemetry error handler and retried until `ctx` is canceled or
`shutdown` is called. Canceling `ctx` also shuts down an initialized provider.

This reads:

- `INDEXER_STATSD_ADDR`: OTLP/gRPC endpoint.
- `INDEXER_STATSD_PREFIX`: metric prefix and OpenTelemetry `service.name`.
- `INDEXER_STATSD_OTEL_INSECURE`: disable transport security.
- `INDEXER_STATSD_TRACING_ENABLED`: enable trace export; defaults to `true`.
- `INDEXER_STATSD_DISABLED`: disable metrics, tracing, exporters, and client
  connections; defaults to `true`.

Legacy counter mode, mocking, Datadog, Mixpanel, and Bugsnag variables are not
used.

For explicit configuration, use `metrics.Init(ctx, metrics.Config{...})` or
`metrics.InitBackground(ctx, metrics.Config{...}, retryInterval)`.

## Counters

Use `Counter` for unsigned count values and `Incr` to increment by one.

```go
metrics.Counter("orders.processed", uint64(count), "market", marketID)
metrics.Incr("orders.accepted", "market", marketID)
```

## Gauges

```go
metrics.Gauge("queue.depth", float64(depth), "queue", queueName)
```

## Histograms

`Histogram` records durations in milliseconds.

```go
metrics.Histogram("request.latency", time.Since(start), "route", routeName)
```

## Events

Use `Event` to bind tags whose final values are only known when the metric is
emitted:

```go
func ProcessOrder(marketID string) (result string, err error) {
	defer metrics.Event("order.processed", "market", marketID).
		BindErr(&err).
		Bind("result", &result).
		Incr()

	result = "accepted"
	return result, nil
}
```

## Duration recording

`Record` measures an operation and emits its duration as a histogram when
`Done` runs. `Done` is idempotent.

```go
func ProcessOrder(ctx context.Context, marketID string) (err error) {
	defer metrics.Record("order.process").
		BindErr(&err).
		Done("market", marketID)

	return process(ctx, marketID)
}
```

Pointer values passed to `Bind` are resolved when the metric is emitted.

## Spans

`WithSpan` uses the global OpenTelemetry tracer provider. `Context` returns the
span-bearing context.

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
