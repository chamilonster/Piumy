// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
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
// read-alias, so an old config keeps working without an edit (2026-07-08).
func TestLoadNormalizesAdvancedAlias(t *testing.T) {
	jid := "56955147132@s.whatsapp.net"
	path := writeRouter(t, `{
		"allow_all": false,
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
	if !d.Allowed {
		t.Fatalf("Resolve(%s): expected allowed", jid)
	}
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
	path := writeRouter(t, `{"allow_all": true, "default_mode": "dedicated"}`)
	cfg := Load(path)
	if cfg.DefaultMode != "dedicated" {
		t.Fatalf("DefaultMode: got %q, want %q", cfg.DefaultMode, "dedicated")
	}
	if d := cfg.Resolve("anyone@s.whatsapp.net"); d.Mode != "dedicated" {
		t.Fatalf("Resolve.Mode: got %q, want %q", d.Mode, "dedicated")
	}
}

// TestLoadMissingFileDefaultsDedicated: no router.json → safe default is
// 'dedicated' (whitelist-only), never the legacy 'advanced'.
func TestLoadMissingFileDefaultsDedicated(t *testing.T) {
	cfg := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if cfg.DefaultMode != "dedicated" {
		t.Fatalf("DefaultMode: got %q, want %q", cfg.DefaultMode, "dedicated")
	}
	if cfg.AllowAll {
		t.Fatal("missing router.json must default to whitelist-only (allow_all=false)")
	}
}
