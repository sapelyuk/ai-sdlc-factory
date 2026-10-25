// Package routing tests cooldown backoff per ADR-021.
package routing

import (
	"testing"
	"time"
)

func TestNextCooldown(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		minD     time.Duration
		maxD     time.Duration
	}{
		{"cf=1: 60s base", 1, 60 * time.Second, 66 * time.Second},
		{"cf=2: 120s", 2, 120 * time.Second, 132 * time.Second},
		{"cf=3: 240s", 3, 240 * time.Second, 264 * time.Second},
		{"cf=4: 480s", 4, 480 * time.Second, 528 * time.Second},
		{"cf=5: 960s", 5, 960 * time.Second, 1056 * time.Second},
		{"cf=6: 1920s", 6, 1920 * time.Second, 2112 * time.Second},
		{"cf=7: 3840s", 7, 3840 * time.Second, 4224 * time.Second},
		// 60s << 7 = 7680s = 2h8m — cap does not trigger; jitter = d/10 ≈ 12m48s
		{"cf=8: ~2h8m (uncapped)", 8, 7680 * time.Second, 7680*time.Second + 13*time.Minute},
		// 60s << 8 = 15360s > 4h — cap triggers, and jitter never pushes
		// the result past CooldownMax.
		{"cf=9: 4h (capped)", 9, 4 * time.Hour, 4 * time.Hour},
		{"cf=10: 4h (capped)", 10, 4 * time.Hour, 4 * time.Hour},
		{"cf=100: 4h (capped)", 100, 4 * time.Hour, 4 * time.Hour},
		{"cf=0: treated as 1", 0, 60 * time.Second, 66 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < 10; i++ {
				d := NextCooldown(tt.failures)
				if d < tt.minD || d > tt.maxD {
					t.Errorf("NextCooldown(%d) = %v, want in [%v, %v]",
						tt.failures, d, tt.minD, tt.maxD)
				}
			}
		})
	}
}
