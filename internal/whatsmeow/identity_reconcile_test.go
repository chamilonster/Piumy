package whatsmeow

import (
	"context"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"piumy-gateway/internal/store"
)

// TestReconcileIdentitiesOnceNoOpWhenSettingDisabled is S13's own safety
// regression (ct-2026-07-30-1835): the sweep is wired to run from boot
// (Start), but the regla dura is "nada destructivo sin OK explícito del
// boss y con backup verificado" — SettingIdentityAutoReconcile's default
// (false) must make this a true no-op, even with a real, resolvable @lid
// duplicate sitting right there ready to merge.
func TestReconcileIdentitiesOnceNoOpWhenSettingDisabled(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const lidJID = "555@lid"
	const numberJID = "55500000020@s.whatsapp.net"
	if err := st.TouchChat(lidJID, "Alguien", 10); err != nil {
		t.Fatal(err)
	}

	client := newTestWmeowClient(t)
	lid := types.NewJID("555", "lid")
	number := types.NewJID("55500000020", "s.whatsapp.net")
	if err := client.Store.LIDs.PutLIDMapping(context.Background(), lid, number); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{store: st, client: client}
	a.reconcileIdentitiesOnce(context.Background())

	if _, ok, err := st.GetChat(lidJID); err != nil || !ok {
		t.Errorf("GetChat(%q) ok=%v err=%v, want it UNTOUCHED — the setting defaults to off", lidJID, ok, err)
	}
}

// TestReconcileIdentitiesOnceMergesWhenSettingEnabled proves the mechanism
// actually works once flipped on — the same pairing as above, but with
// SettingIdentityAutoReconcile explicitly enabled.
func TestReconcileIdentitiesOnceMergesWhenSettingEnabled(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSettingBool(store.SettingIdentityAutoReconcile, true); err != nil {
		t.Fatal(err)
	}

	const lidJID = "666@lid"
	const numberJID = "55500000025@s.whatsapp.net"
	if err := st.TouchChat(lidJID, "Alguien Más", 10); err != nil {
		t.Fatal(err)
	}

	client := newTestWmeowClient(t)
	lid := types.NewJID("666", "lid")
	number := types.NewJID("55500000025", "s.whatsapp.net")
	if err := client.Store.LIDs.PutLIDMapping(context.Background(), lid, number); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{store: st, client: client}
	a.reconcileIdentitiesOnce(context.Background())

	if _, ok, err := st.GetChat(lidJID); err != nil || ok {
		t.Errorf("GetChat(%q) ok=%v err=%v, want it merged away (setting enabled)", lidJID, ok, err)
	}
	c, ok, err := st.GetChat(numberJID)
	if err != nil || !ok || c.Name != "Alguien Más" {
		t.Errorf("GetChat(%q) = %+v ok=%v err=%v, want the renamed/merged chat", numberJID, c, ok, err)
	}
}
