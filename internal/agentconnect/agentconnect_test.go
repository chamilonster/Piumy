package agentconnect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteKeysFromConfig(t *testing.T) {
	dir := t.TempDir()
	if err := Write(Params{
		DataDir: dir, MCPAddr: ":8091", RESTAddr: ":8092",
		MCPKey: "mcp-secret", RESTKey: "rest-secret",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got Info
	readInfo(t, dir, &got)

	want := Info{
		MCPURL:  "http://127.0.0.1:8091/mcp",
		RESTURL: "http://127.0.0.1:8092",
		MCPKey:  "mcp-secret",
		RESTKey: "rest-secret",
	}
	if got != want {
		t.Errorf("Info = %+v, want %+v", got, want)
	}
}

func TestWriteMissingKeyStaysEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := Write(Params{DataDir: dir, MCPAddr: ":8091", RESTAddr: ":8092", MCPKey: "mcp-secret"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got Info
	readInfo(t, dir, &got)

	if got.RESTKey != "" {
		t.Errorf("RESTKey = %q, want empty (rest_key was never set)", got.RESTKey)
	}
	if got.MCPKey != "mcp-secret" {
		t.Errorf("MCPKey = %q, want mcp-secret", got.MCPKey)
	}

	// The field itself must still be present in the JSON, not omitted.
	raw, err := os.ReadFile(filepath.Join(dir, "agent-connect.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := m["rest_key"]; !ok || v != "" {
		t.Errorf("rest_key field = %v (ok=%v), want present and empty", v, ok)
	}
}

func TestWriteExplicitHostKept(t *testing.T) {
	dir := t.TempDir()
	if err := Write(Params{DataDir: dir, MCPAddr: "192.168.1.5:8091", RESTAddr: "0.0.0.0:8092"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got Info
	readInfo(t, dir, &got)

	if got.MCPURL != "http://192.168.1.5:8091/mcp" {
		t.Errorf("MCPURL = %q, want explicit host kept", got.MCPURL)
	}
	if got.RESTURL != "http://127.0.0.1:8092" {
		t.Errorf("RESTURL = %q, want 0.0.0.0 mapped to loopback", got.RESTURL)
	}
}

func TestWriteIPv6WildcardMapsToLoopback(t *testing.T) {
	dir := t.TempDir()
	if err := Write(Params{DataDir: dir, MCPAddr: "[::]:8091", RESTAddr: "[::]:8092"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var got Info
	readInfo(t, dir, &got)

	if got.MCPURL != "http://127.0.0.1:8091/mcp" {
		t.Errorf("MCPURL = %q, want [::] mapped to loopback", got.MCPURL)
	}
	if got.RESTURL != "http://127.0.0.1:8092" {
		t.Errorf("RESTURL = %q, want [::] mapped to loopback", got.RESTURL)
	}
}

func TestWriteCreatesDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "datadir")
	if err := Write(Params{DataDir: dir, MCPAddr: ":8091", RESTAddr: ":8092"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-connect.json")); err != nil {
		t.Errorf("agent-connect.json not created in nested dir: %v", err)
	}
}

func readInfo(t *testing.T, dir string, out *Info) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "agent-connect.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}
