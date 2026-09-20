package config

import (
	"path/filepath"
	"testing"
	"time"
)

// TestLoadDBPathDefaultsUnderDataDirWhenUnset is T169's own regression
// (ct-2026-09-19-1433): DBPath used to be the ONE field that couldn't leak
// into the working directory by accident, because Load() refused to start
// without PIUMY_DB_PATH set at all. Now every data path, DBPath included,
// resolves under DataDir()/secrets instead — never relative, never
// required, never a hard error for something the operator simply forgot.
func TestLoadDBPathDefaultsUnderDataDirWhenUnset(t *testing.T) {
	t.Setenv("PIUMY_DB_PATH", "")
	dataDir := t.TempDir()
	t.Setenv("PIUMY_DATA_DIR", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with no PIUMY_DB_PATH: want a resolved default, got error: %v", err)
	}
	want := filepath.Join(dataDir, "secrets", "piumy.db")
	if cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q (DataDir()/secrets, T169)", cfg.DBPath, want)
	}
}

func TestLoadDefaults(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("PIUMY_DATA_DIR", dataDir)
	secretsDir := filepath.Join(dataDir, "secrets")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(secretsDir, "router.json"); cfg.RouterPath != want {
		t.Errorf("RouterPath = %q, want %q", cfg.RouterPath, want)
	}
	if want := filepath.Join(secretsDir, "status.json"); cfg.StatusPath != want {
		t.Errorf("StatusPath = %q, want %q", cfg.StatusPath, want)
	}
	if cfg.SwampedAt != 8 {
		t.Errorf("SwampedAt = %d, want 8", cfg.SwampedAt)
	}
	if cfg.RateLimitPerMin != 10 || cfg.RateLimitPerDay != 500 {
		t.Errorf("RateLimitPerMin/Day = %d/%d, want 10/500", cfg.RateLimitPerMin, cfg.RateLimitPerDay)
	}
	if cfg.WifiIface != "wlan0" {
		t.Errorf("WifiIface = %q, want wlan0", cfg.WifiIface)
	}
	if cfg.MCPAddr != ":8091" || cfg.RESTAddr != ":8092" {
		t.Errorf("MCPAddr/RESTAddr = %q/%q, want :8091/:8092", cfg.MCPAddr, cfg.RESTAddr)
	}
	if cfg.RESTKey != "" {
		t.Errorf("RESTKey = %q, want empty (open dev/LAN default)", cfg.RESTKey)
	}
	if cfg.PolicyPath != "" {
		t.Errorf("PolicyPath = %q, want empty (falls back to each package's embedded default)", cfg.PolicyPath)
	}
}

// TestF5AddrEnvOverrides covers PIUMY_MCP_ADDR/REST_ADDR/REST_KEY/POLICY_PATH
// (F5-wire) — env overrides the default listen addrs and the open-by-default
// REST key.
func TestF5AddrEnvOverrides(t *testing.T) {
	t.Setenv("PIUMY_DB_PATH", "piumy.db")
	t.Setenv("PIUMY_MCP_ADDR", ":9001")
	t.Setenv("PIUMY_REST_ADDR", ":9002")
	t.Setenv("PIUMY_REST_KEY", "secret")
	t.Setenv("PIUMY_POLICY_PATH", "/etc/piumy/policy.md")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPAddr != ":9001" || cfg.RESTAddr != ":9002" {
		t.Errorf("MCPAddr/RESTAddr = %q/%q, want :9001/:9002", cfg.MCPAddr, cfg.RESTAddr)
	}
	if cfg.RESTKey != "secret" {
		t.Errorf("RESTKey = %q, want secret", cfg.RESTKey)
	}
	if cfg.PolicyPath != "/etc/piumy/policy.md" {
		t.Errorf("PolicyPath = %q, want /etc/piumy/policy.md", cfg.PolicyPath)
	}
}

// TestLoadAccountSetDefaultsPortsToZero is S1's own case
// (ct-2026-09-20-1100): with PIUMY_ACCOUNT set and no explicit
// PIUMY_MCP_ADDR/PIUMY_REST_ADDR, the port default must be ":0" (OS picks
// a free one) instead of the fixed :8091/:8092 — a second account's
// instance would otherwise fail to bind against the first's.
func TestLoadAccountSetDefaultsPortsToZero(t *testing.T) {
	t.Setenv("PIUMY_DATA_DIR", t.TempDir())
	t.Setenv("PIUMY_ACCOUNT", "trabajo")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPAddr != ":0" || cfg.RESTAddr != ":0" {
		t.Errorf("MCPAddr/RESTAddr = %q/%q, want :0/:0 with PIUMY_ACCOUNT set", cfg.MCPAddr, cfg.RESTAddr)
	}
}

// TestLoadAccountSetExplicitPortEnvStillWins: an operator who pins a port
// by hand must keep that pin even with PIUMY_ACCOUNT set — the ":0" default
// only fills in what nobody asked for explicitly.
func TestLoadAccountSetExplicitPortEnvStillWins(t *testing.T) {
	t.Setenv("PIUMY_DATA_DIR", t.TempDir())
	t.Setenv("PIUMY_ACCOUNT", "trabajo")
	t.Setenv("PIUMY_MCP_ADDR", ":9091")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPAddr != ":9091" {
		t.Errorf("MCPAddr = %q, want :9091 (explicit env must win over the account default)", cfg.MCPAddr)
	}
	if cfg.RESTAddr != ":0" {
		t.Errorf("RESTAddr = %q, want :0 (untouched var still gets the account default)", cfg.RESTAddr)
	}
}

// TestDispatchDelayEnv covers env override and the anti-ban invariant: a
// non-positive value must never be honored (it would mean instant sends).
func TestDispatchDelayEnv(t *testing.T) {
	t.Setenv("PIUMY_DB_PATH", "piumy.db")

	t.Run("defaults when unset", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DispatchDelayMin != 1*time.Second || cfg.DispatchDelayMax != 5*time.Second {
			t.Errorf("DispatchDelayMin/Max = %v/%v, want 1s/5s", cfg.DispatchDelayMin, cfg.DispatchDelayMax)
		}
	})

	t.Run("env overrides", func(t *testing.T) {
		t.Setenv("PIUMY_DELAY_DISPATCH_MIN", "2s")
		t.Setenv("PIUMY_DELAY_DISPATCH_MAX", "9s")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DispatchDelayMin != 2*time.Second || cfg.DispatchDelayMax != 9*time.Second {
			t.Errorf("DispatchDelayMin/Max = %v/%v, want 2s/9s", cfg.DispatchDelayMin, cfg.DispatchDelayMax)
		}
	})

	t.Run("non-positive falls back to default (never instant)", func(t *testing.T) {
		t.Setenv("PIUMY_DELAY_DISPATCH_MIN", "0s")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DispatchDelayMin != 1*time.Second {
			t.Errorf("DispatchDelayMin = %v, want default 1s (0 must not be honored)", cfg.DispatchDelayMin)
		}
	})
}
