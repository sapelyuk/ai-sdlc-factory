// Package routing implements profile-based routing with per-(provider, model)
// fallback and cooldown, per ADR-021.
package routing

import (
	"math/rand"
	"time"
)

// Cooldown backoff parameters (ADR-021: 60s → 4h, with jitter).
const (
	CooldownBase = 60 * time.Second
	CooldownMax  = 4 * time.Hour
	// maxShift caps the exponential shift so base<<shift does not overflow
	// and saturates slightly above CooldownMax (60s << 8 ≈ 4.27h).
	maxShift = 8
)

// NextCooldown returns the cooldown duration for the given number of
// consecutive failures (1-based): 60s * 2^(failures-1) with up to 10%
// positive jitter, capped at 4h including the jitter so the result never
// exceeds CooldownMax.
func NextCooldown(consecutiveFailures int) time.Duration {
	if consecutiveFailures < 1 {
		consecutiveFailures = 1
	}
	shift := consecutiveFailures - 1
	if shift > maxShift {
		shift = maxShift
	}
	d := CooldownBase << shift
	// Jitter the base duration, then apply the hard cap to the final value.
	jitter := time.Duration(rand.Int63n(int64(d / 10)))
	return min(d+jitter, CooldownMax)
}
