// SPDX-License-Identifier: AGPL-3.0-only
package governor

import (
	"context"
	"testing"
	"time"
)

func TestNewDelayWindowFallback(t *testing.T) {
	cases := []struct {
		name           string
		min, max       time.Duration
		defMin, defMax time.Duration
		wantMin        time.Duration
		wantMax        time.Duration
	}{
		{"valid passthrough", 2 * time.Second, 6 * time.Second, time.Second, 5 * time.Second, 2 * time.Second, 6 * time.Second},
		{"non-positive min falls back", 0, 6 * time.Second, time.Second, 5 * time.Second, time.Second, 6 * time.Second},
		{"non-positive max falls back", 2 * time.Second, 0, time.Second, 5 * time.Second, 2 * time.Second, 5 * time.Second},
		{"inverted max falls back", 4 * time.Second, 1 * time.Second, time.Second, 5 * time.Second, 4 * time.Second, 5 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := NewDelayWindow(c.min, c.max, c.defMin, c.defMax)
			if w.Min != c.wantMin || w.Max != c.wantMax {
				t.Errorf("got Min=%v Max=%v, want Min=%v Max=%v", w.Min, w.Max, c.wantMin, c.wantMax)
			}
		})
	}
}

func TestDelayWindowRandomInBounds(t *testing.T) {
	w := DelayWindow{Min: 10 * time.Millisecond, Max: 30 * time.Millisecond}
	for i := 0; i < 50; i++ {
		d := w.Random()
		if d < w.Min || d >= w.Max {
			t.Fatalf("Random() = %v, want in [%v, %v)", d, w.Min, w.Max)
		}
	}
}

func TestDelayWindowRandomDegenerate(t *testing.T) {
	w := DelayWindow{Min: 5 * time.Millisecond, Max: 5 * time.Millisecond}
	if d := w.Random(); d != w.Min {
		t.Errorf("Max<=Min: Random() = %v, want %v", d, w.Min)
	}
}

func TestDelayWindowSleepCancels(t *testing.T) {
	w := DelayWindow{Min: time.Hour, Max: 2 * time.Hour} // would hang the test if ctx is ignored
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	w.Sleep(ctx)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Sleep did not respect ctx cancellation, took %v", elapsed)
	}
}
