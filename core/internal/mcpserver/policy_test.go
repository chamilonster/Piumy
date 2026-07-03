// SPDX-License-Identifier: AGPL-3.0-only
package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pimywa/internal/governor"
	"pimywa/internal/router"
	"pimywa/internal/state"
	"pimywa/internal/store"
)

func TestDecisionPolicyHashStable(t *testing.T) {
	c1, v1 := decisionPolicy("")
	c2, v2 := decisionPolicy("")
	if v1 != v2 || c1 != c2 {
		t.Errorf("decisionPolicy(\"\") not stable across calls: v1=%q v2=%q", v1, v2)
	}
	if v1 == "" {
		t.Error("policy_version is empty, want a real sha256 hash")
	}
}

// TestDecisionPolicyExternalOverride covers the live-edit requirement:
// an external file at PolicyPath overrides the embedded default, and its
// content change is what changes policy_version — recomputed fresh, not cached.
func TestDecisionPolicyExternalOverride(t *testing.T) {
	_, defaultVersion := decisionPolicy("")

	path := filepath.Join(t.TempDir(), "decision-policy.md")
	if err := os.WriteFile(path, []byte("# Custom policy\nAlways ask the owner first.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, version := decisionPolicy(path)
	if !strings.Contains(content, "Custom policy") {
		t.Errorf("content = %q, want the external file's content", content)
	}
	if version == defaultVersion {
		t.Error("version unchanged after override — should differ from the embedded default")
	}

	// Missing file falls back to the embedded default (fail-safe).
	missing := filepath.Join(t.TempDir(), "does-not-exist.md")
	if c, v := decisionPolicy(missing); v != defaultVersion || c == "" {
		t.Errorf("missing override file: got version=%q, want the default %q", v, defaultVersion)
	}
}

func TestGetDecisionPolicyTool(t *testing.T) {
	_, srv, ctx := newTestServer(t)
	out := callTool(t, ctx, srv, "get_decision_policy", map[string]any{})

	_, wantVersion := decisionPolicy("")
	if !strings.Contains(out, wantVersion) {
		t.Errorf("get_decision_policy = %s, want policy_version %q in it", out, wantVersion)
	}
	if !strings.Contains(out, "NO siempre respondas") {
		t.Errorf("get_decision_policy = %s, want the embedded policy text", out)
	}
}

// TestSendMessageRequiresCurrentPolicyVersion covers the DoD's core gate:
// missing/stale policy_version rejects, the current one passes.
func TestSendMessageRequiresCurrentPolicyVersion(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pimywa.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	routerPath := filepath.Join(dir, "router.json")
	cfg := `{"allow_all":false,"default_mode":"advanced","whitelist":["56955147132@s.whatsapp.net"]}`
	if err := os.WriteFile(routerPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rtMgr := router.NewManager(routerPath)
	sm := state.NewManager(filepath.Join(dir, "status.json"), 8)
	gov := governor.NewLimiter(10, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := st.TouchChat("56955147132@s.whatsapp.net", "Boss", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChatRules("56955147132@s.whatsapp.net", "responder normalmente"); err != nil {
		t.Fatal(err)
	}

	srv := New(ctx, Deps{Store: st, State: sm, Router: rtMgr, Gov: gov, AgentIdle: time.Minute})
	_, currentVersion := decisionPolicy("")

	call := func(args map[string]any) string {
		req, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "send_message", "arguments": args},
		})
		resp := srv.HandleMessage(ctx, req)
		out, _ := json.Marshal(resp)
		return string(out)
	}

	base := map[string]any{"to": "56955147132@s.whatsapp.net", "message": "hola", "model": "claude-opus-4-8"}

	t.Run("missing policy_version rejected", func(t *testing.T) {
		if out := call(base); !strings.Contains(out, "policy_version") {
			t.Errorf("got %s, want a policy_version error", out)
		}
	})

	t.Run("stale policy_version rejected", func(t *testing.T) {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		args["policy_version"] = "0000000000000000000000000000000000000000000000000000000000000000"
		if out := call(args); !strings.Contains(out, "stale/missing policy_version") {
			t.Errorf("got %s, want stale policy_version rejection", out)
		}
	})

	t.Run("current policy_version passes", func(t *testing.T) {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		args["policy_version"] = currentVersion
		if out := call(args); !strings.Contains(out, "queued for sending") {
			t.Errorf("got %s, want it to pass with the current policy_version", out)
		}
	})
}
