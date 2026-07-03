// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenAuthToken covers the DoD directly: 64 hex chars (32 bytes), and
// two calls never collide (crypto/rand actually being used, not a fixed
// stub).
func TestGenAuthToken(t *testing.T) {
	a, err := genAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars (32 bytes)", len(a))
	}
	b, err := genAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two calls produced the same token")
	}
}

// TestReadWriteEnvKey covers the DoD directly (item B):
// missing file/key reads as absent, a fresh key is appended, an existing
// key is replaced IN PLACE (order + other keys preserved), and repeated
// writes never accumulate blank lines -- the exact bug hit during manual
// testing (split on a trailing "\n" leaves a stray "" element that must be
// dropped before appending OR replacing, not just on append).
func TestReadWriteEnvKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pimywa.env")

	// Missing file: absent, not an error.
	v, present, err := readEnvKey(path, "PIMYWA_MCP_KEY")
	if err != nil || present || v != "" {
		t.Fatalf("missing file: v=%q present=%v err=%v, want absent/no-error", v, present, err)
	}

	// Seed with other keys already present (mirrors a real pimywa.env).
	seed := "PIMYWA_API_KEY=foo\nPIMYWA_DASH_PASS=bar\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	// Append a new key: other keys and their order survive.
	if err := setEnvKey(path, "PIMYWA_MCP_KEY", "tok1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "PIMYWA_API_KEY=foo\nPIMYWA_DASH_PASS=bar\nPIMYWA_MCP_KEY=tok1\n"
	if string(data) != want {
		t.Fatalf("after append:\n%q\nwant:\n%q", data, want)
	}
	v, present, err = readEnvKey(path, "PIMYWA_MCP_KEY")
	if err != nil || !present || v != "tok1" {
		t.Fatalf("readEnvKey after append: v=%q present=%v err=%v", v, present, err)
	}

	// Replace in place (rotate): same line count, other keys untouched,
	// and critically -- no stray trailing blank line accumulates.
	if err := setEnvKey(path, "PIMYWA_MCP_KEY", "tok2"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want = "PIMYWA_API_KEY=foo\nPIMYWA_DASH_PASS=bar\nPIMYWA_MCP_KEY=tok2\n"
	if string(data) != want {
		t.Fatalf("after replace:\n%q\nwant:\n%q", data, want)
	}

	// Do it again (a second rotate) -- still no blank-line accumulation.
	if err := setEnvKey(path, "PIMYWA_MCP_KEY", "tok3"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n\n") > 0 {
		t.Fatalf("blank line accumulated after repeated writes: %q", data)
	}
	if strings.Count(string(data), "PIMYWA_MCP_KEY=") != 1 {
		t.Fatalf("expected exactly one PIMYWA_MCP_KEY line, got: %q", data)
	}

	// Fresh file (no seed): creates the parent dir + a clean single line.
	freshPath := filepath.Join(dir, "nested", "pimywa.env")
	if err := setEnvKey(freshPath, "PIMYWA_MCP_KEY", "tok4"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(freshPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "PIMYWA_MCP_KEY=tok4\n" {
		t.Fatalf("fresh file = %q, want a single clean line", data)
	}
}
