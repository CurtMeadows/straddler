// Package worker implements the straddler job processing pool.
package worker

import (
	"math"
	"math/rand/v2"
	"time"
)

// Backoff computes a retry delay using exponential backoff with ±10% jitter.
//
//	delay = clamp(base * 2^(attempt-1), 0, max) ± 10% jitter
//
// attempt is 1-indexed — pass job.AttemptCount directly after it has been
// incremented by ClaimNextJob.
//
// The jitter prevents a thundering herd: if N workers all fail at the same
// moment they won't all wake up and retry at exactly the same instant.
func Backoff(attempt int, base, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	// Compute the exponential component, guarding against float overflow.
	exp := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(base) * exp)

	// Clamp to max (also handles overflow where d goes negative).
	if d > max || d < 0 {
		d = max
	}

	// Add ±10% jitter: pick a random value in [-0.1*d, +0.1*d].
	jitterWindow := float64(d) * 0.1
	jitter := time.Duration((rand.Float64()*2 - 1) * jitterWindow)
	d += jitter

	if d < 0 {
		d = 0
	}
	return d
}
