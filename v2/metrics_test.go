package metrics

import (
	"errors"
	"testing"

	base "github.com/InjectiveLabs/metrics"
	"github.com/stretchr/testify/require"
)

func TestApprovedReportsCompile(t *testing.T) {
	Counter("test.counter", uint64(1), "tag", "value")
	Incr("test.incr", "tag", "value")
}

func TestStartBindsFinalValues(t *testing.T) {
	var err error
	result := "success"
	attempts := 1
	ratio := 1.5
	enabled := false

	f := Record("test.track", "static", "tag").
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

	require.Equal(t, base.Tags{
		"static":   "tag",
		"error":    "true",
		"result":   "failure",
		"attempts": "2",
		"ratio":    "2.5",
		"enabled":  "true",
	}, f.finishTags())
}

func TestStartBindsNilErrorAsFalse(t *testing.T) {
	var err error

	f := Record("test.track").BindErr(&err).(*record)

	require.Equal(t, "false", f.finishTags()["error"])
}

func TestStartDoneDoesNotPanic(t *testing.T) {
	var err error
	result := "success"

	require.NotPanics(t, func() {
		Record("test.track").BindErr(&err).Bind("result", &result).Done()
	})
}

func TestDoneIsIdempotent(t *testing.T) {
	var err error
	result := "success"

	require.NotPanics(t, func() {
		rec := Record("test.track").BindErr(&err).Bind("result", &result)
		rec.Done()
		rec.Done() // Second call should be safe
		rec.Done() // Third call should also be safe
	})
}
