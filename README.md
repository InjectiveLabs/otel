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
- `INDEXER_STATSD_REPORT_CANCELED_AS_ERROR`: classify `context.Canceled` as an
  error; defaults to `false`.
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

`Record` measures duration and emits a histogram. Adding `WithSpan` also creates
a span with the same name. The context returned by `Context` contains that span
and must be passed to child operations so OpenTelemetry can place their spans in
the same trace.

```go
func HandleOrder(ctx context.Context, orderID string) (err error) {
	root := metrics.Record("order.handle", "order_id", orderID).
		WithSpan(ctx).
		BindErr(&err)
	defer root.Done()

	// Every operation receiving this context becomes a child of order.handle.
	ctx = root.Context()

	order, err := loadOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if err = validateOrder(ctx, order); err != nil {
		return err
	}
	return persistOrder(ctx, order)
}

func loadOrder(ctx context.Context, orderID string) (order *Order, err error) {
	rec := metrics.Record("order.load", "order_id", orderID).
		WithSpan(ctx).
		BindErr(&err)
	defer rec.Done()

	// Pass the child context further down if the repository is instrumented.
	return repository.Load(rec.Context(), orderID)
}

func validateOrder(ctx context.Context, order *Order) (err error) {
	rec := metrics.Record("order.validate").
		WithSpan(ctx).
		BindErr(&err).
		Bind("market", &order.Market)
	defer rec.Done()

	return rules.Validate(rec.Context(), order)
}

func persistOrder(ctx context.Context, order *Order) (err error) {
	rec := metrics.Record("order.persist").
		WithSpan(ctx).
		BindErr(&err)
	defer rec.Done("market", order.Market)

	return repository.Save(rec.Context(), order)
}
```

If the initial `ctx` has no span, the resulting trace has one root span and
three child spans:

```text
order.handle
├── order.load
├── order.validate
└── order.persist
```

If the initial context came from instrumented HTTP/gRPC middleware,
`order.handle` is instead a child of the remote span, while all four operations
keep the propagated trace ID.

`Done` ends the span, records the duration histogram, and attaches the final
tags as span attributes. `BindErr` adds an `error=true` or `error=false`
attribute. By default, `context.Canceled` produces `error=false`, while
`context.DeadlineExceeded` remains an error. Set
`REPORT_CANCELED_AS_ERROR=true` if cancellations must count as errors. Because
deferred calls run last-in, first-out, each child span ends before the root
span.

For propagation across services, pass `rec.Context()` to an instrumented
HTTP/gRPC client. On the receiving service, pass the context created by its
OpenTelemetry middleware to `WithSpan`. The propagator configured by `Init`
then keeps both services in the same trace.

When tracing is disabled, `WithSpan` does not create a span and returns the
original context through `Context`; duration metrics continue to work.

For the compact deferred form, `Trace` combines `Record` and `BindCtx`: it
creates a span and replaces the supplied context with its span-bearing context
before `Done` is deferred:

```go
defer metrics.Trace(&runnerCtx, "orchestrator-run", "backend", backend).
	BindErr(&err).
	Done()

return runNextOperation(runnerCtx)
```
