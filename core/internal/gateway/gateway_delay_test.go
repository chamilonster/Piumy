// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"pimywa/internal/store"
)

// TestReceiptKind covers the receipt-type → store-column mapping: delivered
// and read/read-self map to a ts update, everything else (retry, sender,
// played, ...) is explicitly ignored rather than silently mis-recorded.
func TestReceiptKind(t *testing.T) {
	cases := []struct {
		in   types.ReceiptType
		want string
	}{
		{types.ReceiptTypeDelivered, "delivered"},
		{types.ReceiptTypeRead, "read"},
		{types.ReceiptTypeReadSelf, "read"},
		{types.ReceiptTypeRetry, ""},
		{types.ReceiptTypeSender, ""},
		{types.ReceiptTypePlayed, ""},
	}
	for _, c := range cases {
		if got := receiptKind(c.in); got != c.want {
			t.Errorf("receiptKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDefaultConfigDelayWindows covers the anti-ban invariant for the two new
// windows (read/action): min is always > 0, and an inverted/zero max falls
// back rather than producing a broken (max < min) window.
func TestDefaultConfigDelayWindows(t *testing.T) {
	cfg := defaultConfig(Config{})
	if cfg.ReadDelayMin <= 0 || cfg.ReadDelayMax < cfg.ReadDelayMin {
		t.Errorf("zero-value read window = [%v, %v], want a valid positive window", cfg.ReadDelayMin, cfg.ReadDelayMax)
	}
	if cfg.ActionDelayMin <= 0 || cfg.ActionDelayMax < cfg.ActionDelayMin {
		t.Errorf("zero-value action window = [%v, %v], want a valid positive window", cfg.ActionDelayMin, cfg.ActionDelayMax)
	}

	cfg = defaultConfig(Config{ReadDelayMin: 1 * time.Second, ReadDelayMax: 500 * time.Millisecond})
	if cfg.ReadDelayMax < cfg.ReadDelayMin {
		t.Errorf("inverted read window not corrected: [%v, %v]", cfg.ReadDelayMin, cfg.ReadDelayMax)
	}
}

// TestDelayWindowsConsultKVOverride covers the DoD's "aplica en runtime"
// (0753): dispatchDelay/readDelay/actionDelay fall back to cfg when no
// override is set, and reflect a KV-persisted override immediately —
// no restart, no cache to invalidate.
func TestDelayWindowsConsultKVOverride(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	g := &Gateway{msgSt: st, cfg: Config{
		MinSendDelay: 1 * time.Second, MaxSendDelay: 5 * time.Second,
		ReadDelayMin: 2 * time.Second, ReadDelayMax: 8 * time.Second,
		ActionDelayMin: 1 * time.Second, ActionDelayMax: 4 * time.Second,
	}}

	if w := g.dispatchDelay(); w.Min != 1*time.Second || w.Max != 5*time.Second {
		t.Errorf("dispatchDelay() before override = %+v, want cfg's [1s,5s]", w)
	}

	if err := st.SetSettingDuration(store.SettingDispatchDelayMin, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettingDuration(store.SettingDispatchDelayMax, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if w := g.dispatchDelay(); w.Min != 10*time.Second || w.Max != 20*time.Second {
		t.Errorf("dispatchDelay() after override = %+v, want [10s,20s]", w)
	}

	// readDelay/actionDelay untouched — still fall back to cfg.
	if w := g.readDelay(); w.Min != 2*time.Second || w.Max != 8*time.Second {
		t.Errorf("readDelay() = %+v, want cfg's [2s,8s] (no override set)", w)
	}
	if w := g.actionDelay(); w.Min != 1*time.Second || w.Max != 4*time.Second {
		t.Errorf("actionDelay() = %+v, want cfg's [1s,4s] (no override set)", w)
	}
}
