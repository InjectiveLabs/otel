// Package otel exposes a small metrics API backed exclusively by
// OpenTelemetry.
package otel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/InjectiveLabs/otel"

type unsigned interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

type tagSet map[string]string

type tagMapProvider interface {
	ToMap() map[string]string
}

var instruments = instrumentRegistry{
	counters:   make(map[string]otelmetric.Int64Counter),
	gauges:     make(map[string]otelmetric.Float64Gauge),
	histograms: make(map[string]otelmetric.Float64Histogram),
}

var (
	metricsEnabled        atomic.Bool
	tracingEnabled        atomic.Bool
	reportCanceledAsError atomic.Bool
	metricPrefix          atomic.Value
)

type instrumentRegistry struct {
	mu         sync.RWMutex
	counters   map[string]otelmetric.Int64Counter
	gauges     map[string]otelmetric.Float64Gauge
	histograms map[string]otelmetric.Float64Histogram
}

func (r *instrumentRegistry) counter(name string) (otelmetric.Int64Counter, error) {
	name = fullMetricName(name)
	r.mu.RLock()
	counter, ok := r.counters[name]
	r.mu.RUnlock()
	if ok {
		return counter, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if counter, ok = r.counters[name]; ok {
		return counter, nil
	}

	counter, err := otel.Meter(instrumentationName).Int64Counter(name)
	if err != nil {
		return nil, err
	}
	r.counters[name] = counter
	return counter, nil
}

func (r *instrumentRegistry) gauge(name string) (otelmetric.Float64Gauge, error) {
	name = fullMetricName(name)
	r.mu.RLock()
	gauge, ok := r.gauges[name]
	r.mu.RUnlock()
	if ok {
		return gauge, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if gauge, ok = r.gauges[name]; ok {
		return gauge, nil
	}

	gauge, err := otel.Meter(instrumentationName).Float64Gauge(name)
	if err != nil {
		return nil, err
	}
	r.gauges[name] = gauge
	return gauge, nil
}

func (r *instrumentRegistry) histogram(name string) (otelmetric.Float64Histogram, error) {
	name = fullMetricName(name)
	r.mu.RLock()
	histogram, ok := r.histograms[name]
	r.mu.RUnlock()
	if ok {
		return histogram, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if histogram, ok = r.histograms[name]; ok {
		return histogram, nil
	}

	histogram, err := otel.Meter(instrumentationName).Float64Histogram(
		name,
		otelmetric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}
	r.histograms[name] = histogram
	return histogram, nil
}

func (r *instrumentRegistry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = make(map[string]otelmetric.Int64Counter)
	r.gauges = make(map[string]otelmetric.Float64Gauge)
	r.histograms = make(map[string]otelmetric.Float64Histogram)
}

func fullMetricName(name string) string {
	prefix, _ := metricPrefix.Load().(string)
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// Counter reports a monotonic count metric.
func Counter[T unsigned](name string, value T, tags ...any) {
	if !metricsEnabled.Load() {
		return
	}
	counter, err := instruments.counter(name)
	if err != nil {
		return
	}
	counter.Add(
		context.Background(),
		int64(value),
		otelmetric.WithAttributes(attributes(tags...)...),
	)
}

// Incr increments a monotonic count metric by one.
func Incr(name string, tags ...any) {
	Counter(name, uint64(1), tags...)
}

// Gauge reports a value that may move up or down.
func Gauge(name string, value float64, tags ...any) {
	if !metricsEnabled.Load() {
		return
	}
	gauge, err := instruments.gauge(name)
	if err != nil {
		return
	}
	gauge.Record(
		context.Background(),
		value,
		otelmetric.WithAttributes(attributes(tags...)...),
	)
}

// Histogram records a duration in milliseconds.
func Histogram(name string, value time.Duration, tags ...any) {
	if !metricsEnabled.Load() {
		return
	}
	histogram, err := instruments.histogram(name)
	if err != nil {
		return
	}
	histogram.Record(
		context.Background(),
		value.Seconds()*1000,
		otelmetric.WithAttributes(attributes(tags...)...),
	)
}

// Promise binds tags that are resolved when a metric is emitted.
type Promise interface {
	Counter(value uint64, tags ...any)
	Incr(tags ...any)
	Gauge(value float64, tags ...any)
	Histogram(value time.Duration, tags ...any)
	BindErr(*error) Promise
	Bind(key string, value any) Promise
}

type event struct {
	name  string
	tags  tagSet
	binds map[string]any
	err   *error
}

// Event creates a reusable metric event with deferred tag bindings.
func Event(name string, tags ...any) Promise {
	return &event{
		name:  name,
		tags:  combineTags(tags...),
		binds: make(map[string]any),
	}
}

func (e *event) Counter(value uint64, tags ...any) {
	Counter(e.name, value, e.finishTags(tags...))
}

func (e *event) Incr(tags ...any) {
	Incr(e.name, e.finishTags(tags...))
}

func (e *event) Gauge(value float64, tags ...any) {
	Gauge(e.name, value, e.finishTags(tags...))
}

func (e *event) Histogram(value time.Duration, tags ...any) {
	Histogram(e.name, value, e.finishTags(tags...))
}

func (e *event) BindErr(err *error) Promise {
	e.err = err
	return e
}

func (e *event) Bind(key string, value any) Promise {
	e.binds[key] = value
	return e
}

func (e *event) finishTags(tags ...any) tagSet {
	return finishTags(e.tags, e.binds, e.err, tags...)
}

// Recorder measures an operation and optionally creates a tracing span.
type Recorder interface {
	Done(tags ...any)
	BindErr(*error) Recorder
	Bind(key string, value any) Recorder
	WithSpan(ctx context.Context) Recorder
	BindCtx(ctx *context.Context) Recorder
	Context() context.Context
}

type record struct {
	startTime time.Time
	name      string
	err       *error
	tags      tagSet
	binds     map[string]any
	ctx       context.Context
	span      trace.Span
	ctxRef    *context.Context
	parentCtx context.Context
	once      sync.Once
}

// Record starts measuring an operation. Done records its duration once.
func Record(name string, tags ...any) Recorder {
	return &record{
		startTime: time.Now(),
		name:      name,
		tags:      combineTags(tags...),
		binds:     make(map[string]any),
		ctx:       context.Background(),
	}
}

// Trace starts measuring an operation, creates a span, and replaces ctx with
// the span-bearing context until Done restores its previous value.
func Trace(ctx *context.Context, name string, tags ...any) Recorder {
	return Record(name, tags...).BindCtx(ctx)
}

func (r *record) Done(tags ...any) {
	r.once.Do(func() {
		defer r.restoreContext()

		finalTags := r.finishTags(tags...)
		if r.span != nil {
			for key, value := range finalTags {
				r.span.SetAttributes(attribute.String(key, value))
			}
			defer r.span.End()
		}
		Histogram(r.name, time.Since(r.startTime), finalTags)
	})
}

func (r *record) BindErr(err *error) Recorder {
	r.err = err
	return r
}

func (r *record) Bind(key string, value any) Recorder {
	r.binds[key] = value
	return r
}

func (r *record) WithSpan(ctx context.Context) Recorder {
	if ctx == nil {
		ctx = context.Background()
	}
	if !tracingEnabled.Load() {
		r.ctx = ctx
		return r
	}
	if r.span != nil {
		r.span.End()
	}
	r.ctx, r.span = otel.Tracer(instrumentationName).Start(ctx, r.name)
	return r
}

// BindCtx creates a span and replaces ctx with the span-bearing context until
// Done restores its previous value.
func (r *record) BindCtx(ctx *context.Context) Recorder {
	if ctx == nil {
		return r.WithSpan(nil)
	}
	r.ctxRef = ctx
	r.parentCtx = *ctx
	r.WithSpan(*ctx)
	*ctx = r.Context()
	return r
}

func (r *record) restoreContext() {
	if r.ctxRef != nil {
		*r.ctxRef = r.parentCtx
	}
}

func (r *record) Context() context.Context {
	return r.ctx
}

func (r *record) finishTags(tags ...any) tagSet {
	return finishTags(r.tags, r.binds, r.err, tags...)
}

func finishTags(original tagSet, binds map[string]any, err *error, values ...any) tagSet {
	finalTags := mergeTags(original, combineTags(values...))
	if err != nil {
		isError := *err != nil
		if isError &&
			!reportCanceledAsError.Load() &&
			errors.Is(*err, context.Canceled) {
			isError = false
		}
		finalTags["error"] = strconv.FormatBool(isError)
	}
	for key, bound := range binds {
		if value, ok := ToString(bound); ok {
			finalTags[key] = value
		}
	}
	return finalTags
}

func attributes(tags ...any) []attribute.KeyValue {
	tagSet := combineTags(tags...)
	attrs := make([]attribute.KeyValue, 0, len(tagSet))
	for key, value := range tagSet {
		attrs = append(attrs, attribute.String(key, value))
	}
	return attrs
}

func combineTags(values ...any) tagSet {
	tags := make(tagSet)
	var pairs []any

	for _, value := range values {
		switch value := value.(type) {
		case tagSet:
			for key, item := range value {
				tags[key] = item
			}
		case map[string]string:
			for key, item := range value {
				tags[key] = item
			}
		case tagMapProvider:
			for key, item := range value.ToMap() {
				tags[key] = item
			}
		case []any:
			addPairs(tags, value...)
		default:
			pairs = append(pairs, value)
		}
	}

	addPairs(tags, pairs...)
	return tags
}

func addPairs(tags tagSet, pairs ...any) {
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			continue
		}
		if value, ok := ToString(pairs[i+1]); ok {
			tags[key] = value
		}
	}
}

func mergeTags(sources ...tagSet) tagSet {
	dst := make(tagSet)
	for _, source := range sources {
		for key, value := range source {
			dst[key] = value
		}
	}
	return dst
}

// ToString converts the scalar tag values supported by the v2 API.
func ToString(value any) (stringValue string, ok bool) {
	ok = true
	switch value := value.(type) {
	case string:
		stringValue = value
	case int:
		stringValue = strconv.Itoa(value)
	case int8:
		stringValue = strconv.FormatInt(int64(value), 10)
	case int16:
		stringValue = strconv.FormatInt(int64(value), 10)
	case int32:
		stringValue = strconv.FormatInt(int64(value), 10)
	case int64:
		stringValue = strconv.FormatInt(value, 10)
	case uint:
		stringValue = strconv.FormatUint(uint64(value), 10)
	case uint8:
		stringValue = strconv.FormatUint(uint64(value), 10)
	case uint16:
		stringValue = strconv.FormatUint(uint64(value), 10)
	case uint32:
		stringValue = strconv.FormatUint(uint64(value), 10)
	case uint64:
		stringValue = strconv.FormatUint(value, 10)
	case float32:
		stringValue = strconv.FormatFloat(float64(value), 'f', -1, 32)
	case float64:
		stringValue = strconv.FormatFloat(value, 'f', -1, 64)
	case *string:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = *value
		}
	case *int:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatInt(int64(*value), 10)
		}
	case *int8:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatInt(int64(*value), 10)
		}
	case *int16:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatInt(int64(*value), 10)
		}
	case *int32:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatInt(int64(*value), 10)
		}
	case *int64:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatInt(*value, 10)
		}
	case *uint:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatUint(uint64(*value), 10)
		}
	case *uint8:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatUint(uint64(*value), 10)
		}
	case *uint16:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatUint(uint64(*value), 10)
		}
	case *uint32:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatUint(uint64(*value), 10)
		}
	case *uint64:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatUint(*value, 10)
		}
	case *float32:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatFloat(float64(*value), 'f', -1, 32)
		}
	case *float64:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatFloat(*value, 'f', -1, 64)
		}
	case bool:
		stringValue = strconv.FormatBool(value)
	case *bool:
		if value == nil {
			stringValue = "nil"
		} else {
			stringValue = strconv.FormatBool(*value)
		}
	case fmt.Stringer:
		rv := reflect.ValueOf(value)
		if rv.Kind() == reflect.Ptr && rv.IsNil() {
			stringValue = "nil"
		} else {
			stringValue = value.String()
		}
	case nil:
		stringValue = "nil"
	default:
		ok = false
	}
	return stringValue, ok
}
