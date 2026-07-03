// SPDX-License-Identifier: AGPL-3.0-only
package mcpguard

import (
	"testing"
	"time"
)

func TestCheckAllowsWithinRate(t *testing.T) {
	g := New(Config{RatePerMin: 5, EmitRatePerMin: 5, BlockThreshold: 100})
	for i := 0; i < 5; i++ {
		v := g.Check("client-a", false)
		if !v.Allowed {
			t.Fatalf("call %d: got throttled (%q), want allowed within rate", i, v.Reason)
		}
	}
}

func TestCheckThrottlesOverRate(t *testing.T) {
	g := New(Config{RatePerMin: 2, EmitRatePerMin: 2, BlockThreshold: 100})
	g.Check("client-a", false)
	g.Check("client-a", false)
	v := g.Check("client-a", false) // 3rd call exhausts the 2-token bucket
	if v.Allowed {
		t.Fatal("3rd call within the same window: want throttled, got allowed")
	}
}

// TestCheckIsPerClient covers the whole point of identifying by session
// (not by the single shared PIMYWA_MCP_KEY): one flooding client must never
// throttle a different, well-behaved client.
func TestCheckIsPerClient(t *testing.T) {
	g := New(Config{RatePerMin: 1, EmitRatePerMin: 1, BlockThreshold: 100})
	g.Check("flooder", false)
	if v := g.Check("flooder", false); v.Allowed {
		t.Fatal("flooder's 2nd call: want throttled")
	}
	if v := g.Check("well-behaved", false); !v.Allowed {
		t.Fatalf("a different client's 1st call: want allowed, got throttled (%q)", v.Reason)
	}
}

// TestEmitBucketIsStricter covers the "atención especial" requirement:
// send_message/escalate must throttle sooner than general calls even when
// the general bucket still has room.
func TestEmitBucketIsStricter(t *testing.T) {
	g := New(Config{RatePerMin: 100, EmitRatePerMin: 1, BlockThreshold: 100})
	if v := g.Check("client-a", true); !v.Allowed {
		t.Fatalf("1st emit call: want allowed, got %q", v.Reason)
	}
	if v := g.Check("client-a", true); v.Allowed {
		t.Fatal("2nd emit call within the same minute: want throttled by the emit-specific cap")
	}
	// The general (non-emit) bucket is untouched by the emit throttle.
	if v := g.Check("client-a", false); !v.Allowed {
		t.Fatalf("non-emit call after emit throttle: want allowed, got %q", v.Reason)
	}
}

// TestCircuitBreakerBlocksAfterThreshold covers the "bloqueo
// temporal estilo circuit-breaker" requirement.
func TestCircuitBreakerBlocksAfterThreshold(t *testing.T) {
	g := New(Config{RatePerMin: 1, EmitRatePerMin: 1, BlockThreshold: 3, BlockCooldown: time.Minute})
	g.Check("flooder", false) // consumes the only token
	for i := 0; i < 3; i++ {
		g.Check("flooder", false) // throttled 3 times → trips the breaker
	}
	v := g.Check("flooder", false)
	if v.Allowed {
		t.Fatal("after tripping the breaker: want blocked")
	}
	if v.Reason == "" || v.Reason == "rate limited, slow down" {
		t.Errorf("blocked verdict reason = %q, want a distinct 'blocked' message (not the plain throttle message)", v.Reason)
	}
}

// TestUnknownClientsShareABucket covers the fail-safe fallback: a caller
// the middleware can't identify (no session) still gets rate-limited
// (never unlimited), via one shared bucket rather than a hard block.
func TestUnknownClientsShareABucket(t *testing.T) {
	g := New(Config{RatePerMin: 1, EmitRatePerMin: 1, BlockThreshold: 100})
	if v := g.Check("", false); !v.Allowed {
		t.Fatalf("1st unidentified call: want allowed, got %q", v.Reason)
	}
	if v := g.Check("", false); v.Allowed {
		t.Fatal("2nd unidentified call within the window: want throttled (shared bucket, not unlimited)")
	}
}

func TestSetters(t *testing.T) {
	g := New(Config{})
	g.SetRatePerMin(7)
	g.SetEmitRatePerMin(3)
	g.SetBlockThreshold(9)
	g.SetBlockCooldown(2 * time.Minute)
	if g.RatePerMin() != 7 || g.EmitRatePerMin() != 3 || g.BlockThreshold() != 9 || g.BlockCooldown() != 2*time.Minute {
		t.Fatalf("getters after Set* = %d/%d/%d/%s, want 7/3/9/2m0s",
			g.RatePerMin(), g.EmitRatePerMin(), g.BlockThreshold(), g.BlockCooldown())
	}
}

func TestStatusReportsClients(t *testing.T) {
	g := New(Config{RatePerMin: 1, EmitRatePerMin: 1, BlockThreshold: 2, BlockCooldown: time.Minute})
	g.Check("a", false)
	g.Check("a", false) // throttled: 1 throttle hit
	g.Check("a", false) // throttled again: trips the breaker (threshold=2)

	st := g.Status()
	if st.RatePerMin != 1 {
		t.Errorf("Status.RatePerMin = %d, want 1", st.RatePerMin)
	}
	if len(st.Clients) != 1 {
		t.Fatalf("Status.Clients = %v, want exactly 1 tracked client", st.Clients)
	}
	c := st.Clients[0]
	if c.Key != "a" || !c.Blocked || c.BlockedUntil.IsZero() {
		t.Errorf("client status = %+v, want key=a blocked=true with a non-zero BlockedUntil", c)
	}
}

// TestNewDefaultsZeroConfig covers that a zero Config is safe to construct
// (main.go / mcpserver.New falls back to this when nothing is wired) and
// still actually protects — never an unlimited-passthrough by accident.
func TestNewDefaultsZeroConfig(t *testing.T) {
	g := New(Config{})
	if g.RatePerMin() <= 0 || g.EmitRatePerMin() <= 0 || g.BlockThreshold() <= 0 || g.BlockCooldown() <= 0 {
		t.Fatalf("zero Config produced non-positive defaults: rate=%d emit=%d threshold=%d cooldown=%s",
			g.RatePerMin(), g.EmitRatePerMin(), g.BlockThreshold(), g.BlockCooldown())
	}
}
