// SPDX-License-Identifier: AGPL-3.0-only
package config

import (
	"testing"
	"time"
)

// TestDispatchDelayEnv covers the bug fix: MinSendDelay/MaxSendDelay used to
// never reach gateway.Config from env at all. Unset = default 1s/5s (matches
// the gateway's own fallback); set = honored; non-positive = default (anti-ban,
// never instant).
func TestDispatchDelayEnv(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		cfg := Load()
		if cfg.DispatchDelayMin != 1*time.Second {
			t.Errorf("DispatchDelayMin = %v, want 1s", cfg.DispatchDelayMin)
		}
		if cfg.DispatchDelayMax != 5*time.Second {
			t.Errorf("DispatchDelayMax = %v, want 5s", cfg.DispatchDelayMax)
		}
	})

	t.Run("env overrides", func(t *testing.T) {
		t.Setenv("PIMYWA_DELAY_DISPATCH_MIN", "2s")
		t.Setenv("PIMYWA_DELAY_DISPATCH_MAX", "9s")
		cfg := Load()
		if cfg.DispatchDelayMin != 2*time.Second {
			t.Errorf("DispatchDelayMin = %v, want 2s", cfg.DispatchDelayMin)
		}
		if cfg.DispatchDelayMax != 9*time.Second {
			t.Errorf("DispatchDelayMax = %v, want 9s", cfg.DispatchDelayMax)
		}
	})

	t.Run("non-positive falls back to default (never instant)", func(t *testing.T) {
		t.Setenv("PIMYWA_DELAY_DISPATCH_MIN", "0s")
		cfg := Load()
		if cfg.DispatchDelayMin != 1*time.Second {
			t.Errorf("DispatchDelayMin = %v, want default 1s (0 must not be honored)", cfg.DispatchDelayMin)
		}
	})
}
