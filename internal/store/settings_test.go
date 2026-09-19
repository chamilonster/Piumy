package store

import (
	"path/filepath"
	"testing"
)

func TestRotateDashSessionSecretChangesValue(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	before, _ := st.KVGet(SettingDashSessionSecret)
	if before != "" {
		t.Fatalf("SettingDashSessionSecret = %q before any rotation, want empty", before)
	}

	if err := st.RotateDashSessionSecret(); err != nil {
		t.Fatal(err)
	}
	first, err := st.KVGet(SettingDashSessionSecret)
	if err != nil || first == "" {
		t.Fatalf("KVGet after rotate = %q, err=%v, want a non-empty secret", first, err)
	}

	if err := st.RotateDashSessionSecret(); err != nil {
		t.Fatal(err)
	}
	second, err := st.KVGet(SettingDashSessionSecret)
	if err != nil || second == "" {
		t.Fatalf("KVGet after second rotate = %q, err=%v, want a non-empty secret", second, err)
	}
	if first == second {
		t.Error("RotateDashSessionSecret produced the same secret twice — rotation must invalidate previously issued cookies")
	}
}
