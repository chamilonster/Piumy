package sysinfo

import (
	"runtime"
	"testing"
)

// TestCPUPercentBounds covers both platform paths: on Linux CI/deploy it
// exercises the real /proc/loadavg read; on a non-Linux dev host it proves
// the "no reading, never fake a number" degrade path for real, without
// mocking anything.
func TestCPUPercentBounds(t *testing.T) {
	pct, ok := CPUPercent()
	if runtime.GOOS != "linux" {
		if ok {
			t.Fatalf("expected ok=false on %s, got pct=%d", runtime.GOOS, pct)
		}
		return
	}
	if !ok {
		t.Skip("no /proc/loadavg available on this Linux host")
	}
	if pct < 0 || pct > 100 {
		t.Fatalf("CPUPercent out of bounds: %d", pct)
	}
}

func TestRAMPercentBounds(t *testing.T) {
	pct, ok := RAMPercent()
	if runtime.GOOS != "linux" {
		if ok {
			t.Fatalf("expected ok=false on %s, got pct=%d", runtime.GOOS, pct)
		}
		return
	}
	if !ok {
		t.Skip("no /proc/meminfo available on this Linux host")
	}
	if pct < 0 || pct > 100 {
		t.Fatalf("RAMPercent out of bounds: %d", pct)
	}
}

func TestClampPct(t *testing.T) {
	cases := map[float64]int{-5: 0, 0: 0, 50.7: 50, 100: 100, 150: 100}
	for in, want := range cases {
		if got := clampPct(in); got != want {
			t.Errorf("clampPct(%v) = %d, want %d", in, got, want)
		}
	}
}
