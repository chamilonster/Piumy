package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
)

func TestCanOpenAnotherAccountIsOffWhenDataDirIsPinned(t *testing.T) {
	t.Setenv("PIUMY_DATA_DIR", "")
	if !CanOpenAnotherAccount() {
		t.Error("CanOpenAnotherAccount() = false with PIUMY_DATA_DIR empty, want true")
	}
	t.Setenv("PIUMY_DATA_DIR", t.TempDir())
	if CanOpenAnotherAccount() {
		t.Error("CanOpenAnotherAccount() = true with PIUMY_DATA_DIR set, want false (DataDir ignores the account there)")
	}
}

func TestReserveAccountInPicksFirstFreeNameAndCreatesItsFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts") // root itself doesn't exist yet: first run

	got, err := reserveAccountIn(root)
	if err != nil {
		t.Fatalf("reserveAccountIn: %v", err)
	}
	if got != "cuenta-2" {
		t.Errorf("first name = %q, want cuenta-2 (N starts at 2: the default account is the first)", got)
	}
	if fi, err := os.Stat(filepath.Join(root, "cuenta-2")); err != nil || !fi.IsDir() {
		t.Errorf("accounts/cuenta-2 not created as a folder: %v", err)
	}

	got, err = reserveAccountIn(root)
	if err != nil || got != "cuenta-3" {
		t.Errorf("second call = %q, %v; want cuenta-3 (cuenta-2 is now taken)", got, err)
	}
}

// The contract's own example: cuenta-2 already exists -> cuenta-3. Plus the
// gap case ("primera libre", not "máxima + 1"): cuenta-2 and cuenta-4 exist,
// cuenta-3 was deleted by hand -> cuenta-3.
func TestReserveAccountInSkipsExistingFoldersAndFillsGaps(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"cuenta-2", "cuenta-4"} {
		if err := os.Mkdir(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := reserveAccountIn(root)
	if err != nil || got != "cuenta-3" {
		t.Errorf("got %q, %v; want cuenta-3 (first free, not max+1)", got, err)
	}
}

// Two clicks on the tray before either launched Piumy created its folder:
// with "look, then create later" both would answer cuenta-2.
func TestReserveAccountInConcurrentCallsGetDistinctNames(t *testing.T) {
	root := t.TempDir()
	const n = 8
	names := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name, err := reserveAccountIn(root)
			if err != nil {
				t.Errorf("reserveAccountIn: %v", err)
			}
			names[i] = name
		}(i)
	}
	wg.Wait()
	sort.Strings(names)
	want := []string{"cuenta-2", "cuenta-3", "cuenta-4", "cuenta-5", "cuenta-6", "cuenta-7", "cuenta-8", "cuenta-9"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v (each caller must get its own)", names, want)
		}
	}
}

// ReserveAccount reads the OS root, NOT DataDir(): a Piumy that is itself
// cuenta-2 (PIUMY_ACCOUNT set) must still reserve under <root>/accounts.
func TestReserveAccountUsesOSRootEvenWhenRunningAsANamedAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIUMY_ACCOUNT", "cuenta-2")

	var accounts string
	switch runtime.GOOS {
	case "windows":
		accounts = filepath.Join(home, "Piumy", "accounts")
	case "darwin":
		accounts = filepath.Join(home, "Library", "Application Support", "Piumy", "accounts")
	default:
		accounts = filepath.Join(home, ".local", "share", "piumy", "accounts")
	}

	got, dir, err := ReserveAccount()
	if err != nil {
		t.Fatalf("ReserveAccount: %v", err)
	}
	if got != "cuenta-2" {
		t.Errorf("got %q, want cuenta-2 (nothing under %s yet)", got, accounts)
	}
	if want := filepath.Join(accounts, "cuenta-2"); dir != want {
		t.Errorf("dir = %q, want %q (the folder that was just reserved)", dir, want)
	}
	if _, err := os.Stat(filepath.Join(accounts, "cuenta-2")); err != nil {
		t.Errorf("folder not created under the OS root's accounts/: %v", err)
	}
}

// Literal names on purpose, NOT ranged over accountOwnedPathVars: a test that
// reads the same map the code reads can't notice the map being emptied.
func TestEnvForNewAccountDropsWhatANamedAccountMustNotInherit(t *testing.T) {
	env := []string{
		"PIUMY_DB_PATH=C:\\live\\piumy.db",
		"PIUMY_WA_DB_PATH=C:\\live\\whatsmeow.db",
		"PIUMY_ROUTER_PATH=C:\\live\\router.json",
		"PIUMY_STATUS_PATH=C:\\live\\status.json",
		"PIUMY_MEDIA_DIR=C:\\live\\media",
		"PIUMY_BACKUP_DIR=C:\\live\\backups",
		"PIUMY_DATA_DIR=C:\\live",
		"PIUMY_MCP_ADDR=:8091",
		"PIUMY_REST_ADDR=:8092",
		"piumy_db_path=C:\\live\\lowercase.db", // Windows env names are case-insensitive
		"PIUMY_MCP_KEY=aaaa1111",               // keys still apply to a named account (filedefaults.go)
		"PIUMY_REST_KEY=bbbb2222",
		"PATH=C:\\Windows",
		"=C:=C:\\live", // Windows' hidden per-drive cwd entry: empty name
	}
	want := []string{
		"PIUMY_MCP_KEY=aaaa1111",
		"PIUMY_REST_KEY=bbbb2222",
		"PATH=C:\\Windows",
		"=C:=C:\\live",
	}

	got := EnvForNewAccount(env, "")

	if len(got) != len(want) {
		t.Fatalf("EnvForNewAccount = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The guard for the future: a var added to accountOwnedPathVars (S1's list of
// what a named account owns) must also stop being inherited, with no second
// list to remember to update.
func TestEnvForNewAccountFollowsAccountOwnedPathVars(t *testing.T) {
	for k := range accountOwnedPathVars {
		got := EnvForNewAccount([]string{k + "=x", "KEEP=1"}, "")
		if len(got) != 1 || got[0] != "KEEP=1" {
			t.Errorf("%s survived EnvForNewAccount: %v", k, got)
		}
	}
}

// S5 (ct-2026-09-23-2038): the launcher's login goes out as the seed, and a
// seed the launcher itself inherited never travels on — literal name on
// purpose, the restapi side reads the same constant.
func TestEnvForNewAccountSendsTheLaunchersLoginAndNeverForwardsAStaleOne(t *testing.T) {
	env := []string{"PIUMY_SEED_DASH_HASH=viejo", "KEEP=1"}

	got := EnvForNewAccount(env, "nuevo")
	want := []string{"KEEP=1", "PIUMY_SEED_DASH_HASH=nuevo"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("with a hash: %v, want %v", got, want)
	}

	got = EnvForNewAccount(env, "")
	if len(got) != 1 || got[0] != "KEEP=1" {
		t.Errorf("without a hash: %v, want only KEEP=1 (a stale seed must not be forwarded)", got)
	}
}
