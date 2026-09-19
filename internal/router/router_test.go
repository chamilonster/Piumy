package router

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRouter drops a router.json into a temp dir and returns its path.
func writeRouter(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoadNormalizesAdvancedAlias: a hand-written router.json still carrying
// the pre-rename value 'advanced' must load and resolve as 'dedicated' — the
// read-alias, so an old config keeps working without an edit.
func TestLoadNormalizesAdvancedAlias(t *testing.T) {
	jid := "55500000044@c.us"
	path := writeRouter(t, `{
		"default_mode": "advanced",
		"whitelist": ["`+jid+`"],
		"routes": [
			{"match": "`+jid+`", "mode": "advanced", "plugin": "clevercoder"},
			{"match": "*", "mode": "advanced", "plugin": "none"}
		]
	}`)

	cfg := Load(path)
	if cfg.DefaultMode != "dedicated" {
		t.Fatalf("DefaultMode: got %q, want %q", cfg.DefaultMode, "dedicated")
	}
	for i, r := range cfg.Routes {
		if r.Mode != "dedicated" {
			t.Fatalf("Routes[%d].Mode: got %q, want %q", i, r.Mode, "dedicated")
		}
	}

	d := cfg.Resolve(jid)
	if d.Mode != "dedicated" {
		t.Fatalf("Resolve(%s).Mode: got %q, want %q", jid, d.Mode, "dedicated")
	}
	if d.Plugin != "clevercoder" {
		t.Fatalf("Resolve(%s).Plugin: got %q, want %q", jid, d.Plugin, "clevercoder")
	}
}

// TestLoadDedicatedCanonical: the canonical 'dedicated' value passes through
// untouched (the alias only rewrites 'advanced').
func TestLoadDedicatedCanonical(t *testing.T) {
	path := writeRouter(t, `{"default_mode": "dedicated"}`)
	cfg := Load(path)
	if cfg.DefaultMode != "dedicated" {
		t.Fatalf("DefaultMode: got %q, want %q", cfg.DefaultMode, "dedicated")
	}
	if d := cfg.Resolve("anyone@c.us"); d.Mode != "dedicated" {
		t.Fatalf("Resolve.Mode: got %q, want %q", d.Mode, "dedicated")
	}
}

// TestLoadMissingFileDefaultsDedicated: no router.json -> safe default is
// 'dedicated', never the legacy 'advanced'.
func TestLoadMissingFileDefaultsDedicated(t *testing.T) {
	cfg := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if cfg.DefaultMode != "dedicated" {
		t.Fatalf("DefaultMode: got %q, want %q", cfg.DefaultMode, "dedicated")
	}
}

// TestResolveTerminalIDByRoute covers the new per-route terminal_id: an
// exact-match route's terminal_id resolves; a route with no terminal_id
// leaves Decision.TerminalID empty so the caller falls back to the port
// (see AGENT-BEHAVIOR.md).
func TestResolveTerminalIDByRoute(t *testing.T) {
	jid := "111@c.us"
	other := "222@c.us"
	path := writeRouter(t, `{
		"routes": [
			{"match": "`+jid+`", "terminal_id": "tourmaline"},
			{"match": "`+other+`"}
		]
	}`)
	cfg := Load(path)

	d := cfg.Resolve(jid)
	if d.TerminalID != "tourmaline" {
		t.Errorf("Resolve(%s).TerminalID = %q, want %q", jid, d.TerminalID, "tourmaline")
	}

	d2 := cfg.Resolve(other)
	if d2.TerminalID != "" {
		t.Errorf("Resolve(%s).TerminalID = %q, want empty (no terminal_id on this route, port fallback)", other, d2.TerminalID)
	}
}

// TestResolveTerminalIDExactOverridesWildcard: an exact route's terminal_id
// wins over a catch-all "*" route's terminal_id, same precedence as mode/plugin/model.
func TestResolveTerminalIDExactOverridesWildcard(t *testing.T) {
	jid := "111@c.us"
	path := writeRouter(t, `{
		"routes": [
			{"match": "*", "terminal_id": "default-terminal"},
			{"match": "`+jid+`", "terminal_id": "tourmaline"}
		]
	}`)
	cfg := Load(path)
	if d := cfg.Resolve(jid); d.TerminalID != "tourmaline" {
		t.Errorf("Resolve(%s).TerminalID = %q, want exact match %q to win over wildcard", jid, d.TerminalID, "tourmaline")
	}
	if d := cfg.Resolve("999@c.us"); d.TerminalID != "default-terminal" {
		t.Errorf("Resolve(999).TerminalID = %q, want wildcard fallback %q", d.TerminalID, "default-terminal")
	}
}

// TestUpdateCreatesMissingParentDirectory is T169's own regression
// (ct-2026-09-19-1433): path now usually resolves under config.DataDir(),
// which may not exist yet the first time an operator ever changes a route.
func TestUpdateCreatesMissingParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does", "not", "exist", "yet", "router.json")
	m := NewManager(path)

	if err := m.Update(func(c *Config) { c.DefaultMode = "auto" }); err != nil {
		t.Fatalf("Update with a missing parent directory: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("router.json wasn't actually written: %v", err)
	}
}
