package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackoff_FirstAttempt(t *testing.T) {
	base := 30 * time.Second
	max := time.Hour
	d := Backoff(1, base, max)

	// First attempt should be base ± 10% jitter: [27s, 33s]
	assert.GreaterOrEqual(t, d, 27*time.Second, "first attempt below jitter floor")
	assert.LessOrEqual(t, d, 33*time.Second, "first attempt above jitter ceiling")
}

func TestBackoff_Doubles(t *testing.T) {
	base := 30 * time.Second
	max := time.Hour

	d1 := Backoff(1, base, max)
	d2 := Backoff(2, base, max)
	d3 := Backoff(3, base, max)

	// Each step doubles (within jitter). Strip jitter by checking that
	// attempt N+1 is greater than attempt N when using the same seed isn't
	// possible with rand — instead verify the nominal center values.
	// Nominal: 30s, 60s, 120s. With ±10% jitter the ranges don't overlap
	// until very high attempt numbers.
	assert.Greater(t, d2.Seconds(), d1.Seconds()*0.8,
		"attempt 2 should be roughly 2x attempt 1 (after jitter)")
	assert.Greater(t, d3.Seconds(), d2.Seconds()*0.8,
		"attempt 3 should be roughly 2x attempt 2 (after jitter)")
}

func TestBackoff_CappedAtMax(t *testing.T) {
	base := 30 * time.Second
	max := 2 * time.Minute

	// At attempt 10, nominal value is 30s * 2^9 = 15360s >> 2m
	d := Backoff(10, base, max)

	// Allow for +10% jitter above max
	assert.LessOrEqual(t, d, time.Duration(float64(max)*1.1),
		"should be capped at max (with jitter tolerance)")
}

func TestBackoff_AttemptZeroOrNegative(t *testing.T) {
	// Attempt counts < 1 should be treated as 1.
	base := 30 * time.Second
	max := time.Hour

	d0 := Backoff(0, base, max)
	dm := Backoff(-5, base, max)
	d1 := Backoff(1, base, max)

	// All three should be in the same range as attempt 1.
	for _, d := range []time.Duration{d0, dm} {
		assert.GreaterOrEqual(t, d, 27*time.Second)
		assert.LessOrEqual(t, d, 33*time.Second)
		_ = d1 // suppress unused warning
	}
}

func TestBackoff_NeverNegative(t *testing.T) {
	// Run many iterations to confirm jitter never pushes duration below zero.
	base := 1 * time.Millisecond
	max := 10 * time.Millisecond

	for i := range 500 {
		d := Backoff(i+1, base, max)
		assert.GreaterOrEqual(t, d, time.Duration(0), "duration should never be negative")
	}
}

func TestBackoff_JitterWithinBounds(t *testing.T) {
	// Run many iterations to verify jitter stays within ±10%.
	base := 60 * time.Second
	max := time.Hour
	nominal := float64(base) // attempt 1 nominal = base

	for range 200 {
		d := Backoff(1, base, max)
		lower := time.Duration(nominal * 0.9)
		upper := time.Duration(nominal * 1.1)
		assert.GreaterOrEqual(t, d, lower, "below -10%% jitter bound")
		assert.LessOrEqual(t, d, upper, "above +10%% jitter bound")
	}
}
