//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/config"
)

// envDumpVar puts this test binary in "child" mode: launchAccount starts the
// executable it is given, and the only one a test has at hand is itself. As the
// child it writes its environment where the variable points and exits — before
// the test framework parses "--account".
const envDumpVar = "PIUMY_TEST_ENV_DUMP"

func TestMain(m *testing.M) {
	if dump := os.Getenv(envDumpVar); dump != "" {
		os.WriteFile(dump, []byte(strings.Join(os.Environ(), "\n")), 0o644)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The real launch path, not just the filter it uses: what a new account
// actually receives from a running Piumy (S4 + S5).
func TestLaunchAccountHandsTheChildTheLaunchersLoginAndNotItsData(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "child-env.txt")
	t.Setenv(envDumpVar, dump)
	t.Setenv("PIUMY_DB_PATH", `C:\live\piumy.db`)           // what ApplyFileDefaults leaves in the launcher's env
	t.Setenv(config.DashHashSeedEnv, "hash-viejo-heredado") // a seed the launcher itself inherited
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	if err := launchAccount(exe, "cuenta-2", "$2a$04$hash-del-padre"); err != nil {
		t.Fatalf("launchAccount: %v", err)
	}

	// The child is released, not waited for: poll for what it wrote.
	var env string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(dump); err == nil && len(b) > 0 {
			env = string(b)
			break
		}
	}
	if env == "" {
		t.Fatal("the child never reported its environment")
	}
	lines := strings.Split(env, "\n")
	has := func(line string) bool {
		for _, l := range lines {
			if l == line {
				return true
			}
		}
		return false
	}
	if !has(config.DashHashSeedEnv + "=$2a$04$hash-del-padre") {
		t.Errorf("the child did not get the launcher's login as %s", config.DashHashSeedEnv)
	}
	if has(config.DashHashSeedEnv + "=hash-viejo-heredado") {
		t.Error("a stale inherited seed travelled on to the child")
	}
	if has(`PIUMY_DB_PATH=C:\live\piumy.db`) {
		t.Error("the child inherited the launcher's database path — it would open the live session")
	}
}
