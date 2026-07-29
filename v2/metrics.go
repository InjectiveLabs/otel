// Package metrics exposes the approved v2 client metrics surface.
package metrics

import (
	"context"
	"strconv"
	"sync"
	"time"

	base "github.com/InjectiveLabs/metrics"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/exp/constraints"
)

func init() {
	base.ForceOTelCounters()
	base.DisableStuckFuncReport()
}

// Counter reports a count metric.
func Counter[T constraints.Unsigned](metric string, value T, tags ...interface{}) {
	base.Counter(metric, value, tags...)
}

// Incr increments a count metric by one.
func Incr(metric string, tags ...interface{}) {
	base.Incr(metric, tags...)
}

// Gauge reports a gauge metric.
func Gauge(metric string, value float64, tags ...interface{}) {
	base.Gauge(metric, value, base.Combine(tags...))
}

// Histogram records a value in milliseconds.
func Histogram(metric string, value time.Duration, tags ...interface{}) {
	base.CustomReport(func(s base.Statter, tagSpec []string) {
		_ = s.Histogram(metric, value.Seconds()*1000, tagSpec, 1)
	}, base.Combine(tags...))
}

type Promise interface {
	Counter(value uint64, tags ...interface{})
	Incr(tags ...interface{})
	Gauge(value float64, tags ...interface{})
	Histogram(value time.Duration, tags ...interface{})
	BindErr(*error) Promise
	Bind(key string, value interface{}) Promise
}

type promise struct {
	name  string
	tags  base.Tags
	binds map[string]interface{}
	err   *error
}

func Event(metric string, tags ...interface{}) Promise {
	return &promise{
		name:  metric,
		tags:  base.Combine(tags...),
		binds: make(map[string]interface{}),
	}
}

func (p *promise) Counter(value uint64, tags ...interface{}) {
	Counter(p.name, value, p.finishTags(tags...))
}

func (p *promise) Incr(tags ...interface{}) {
	Incr(p.name, p.finishTags(tags...))
}

func (p *promise) Gauge(value float64, tags ...interface{}) {
	Gauge(p.name, value, p.finishTags(tags...))
}

func (p *promise) Histogram(value time.Duration, tags ...interface{}) {
	Histogram(p.name, value, p.finishTags(tags...))
}

func (p *promise) BindErr(err *error) Promise {
	p.err = err
	return p
}

func (p *promise) Bind(key string, value interface{}) Promise {
	p.binds[key] = value
	return p
}

func (p *promise) finishTags(tags ...interface{}) base.Tags {
	return finishTags(p.tags, p.binds, p.err, tags...)
}

type Recorder interface {
	Done(tags ...interface{})
	BindErr(*error) Recorder
	Bind(key string, value interface{}) Recorder
	WithSpan(ctx context.Context) Recorder
	Context() context.Context
}

func Record(metric string, tags ...interface{}) Recorder {
	return &record{
		name:      metric,
		tags:      base.Combine(tags...),
		startTime: time.Now(),
		ctx:       context.Background(),
		binds:     make(map[string]interface{}),
	}
}

type record struct {
	startTime time.Time
	name      string
	err       *error
	tags      base.Tags
	binds     map[string]interface{}
	ctx       context.Context
	span      trace.Span
	once      sync.Once
}

func (f *record) Done(tags ...interface{}) {
	f.once.Do(func() {
		dur := time.Since(f.startTime)
		t := f.finishTags(tags...)
		f.finishSpan(t)
		Histogram(f.name, dur, t)
	})
}

func (f *record) WithSpan(ctx context.Context) Recorder {
	f.ctx = ctx
	tr := base.Tracer()
	if tr == nil {
		return f
	}
	if f.span != nil {
		f.span.End()
	}
	f.ctx, f.span = tr.Start(ctx, f.name)
	return f
}

func (f *record) Context() context.Context {
	return f.ctx
}

func (f *record) BindErr(err *error) Recorder {
	f.err = err
	return f
}

func (f *record) Bind(key string, value interface{}) Recorder {
	if f.binds == nil {
		f.binds = make(map[string]interface{})
	}
	f.binds[key] = value
	return f
}

func (f *record) finishTags(tags ...interface{}) base.Tags {
	return finishTags(f.tags, f.binds, f.err, tags...)
}

func finishTags(original base.Tags, binds map[string]interface{}, err *error, tags ...interface{}) base.Tags {
	all := base.Combine(tags...)
	if err != nil {
		all["error"] = strconv.FormatBool(*err != nil)
	}
	for k, v := range binds {
		value, ok := base.ToString(v)
		if !ok {
			continue
		}
		all[k] = value
	}
	return base.MergeTags(original, all)
}

func (f *record) finishSpan(tags base.Tags) {
	if f.span == nil {
		return
	}
	defer f.span.End()
	for k, v := range tags {
		f.span.SetAttributes(attribute.String(k, v))
	}
}
