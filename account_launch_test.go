package main

import (
	"os"
	"testing"
)

func TestApplyAccountFlagWinsOverExistingEnv(t *testing.T) {
	t.Setenv("PIUMY_ACCOUNT", "de-la-variable")

	if err := applyAccountFlag([]string{"--account", "cuenta-2"}); err != nil {
		t.Fatalf("applyAccountFlag: %v", err)
	}
	if got := os.Getenv("PIUMY_ACCOUNT"); got != "cuenta-2" {
		t.Errorf("PIUMY_ACCOUNT = %q, want cuenta-2 (the flag wins over the env)", got)
	}
}

func TestApplyAccountFlagAcceptsEqualsForm(t *testing.T) {
	t.Setenv("PIUMY_ACCOUNT", "")

	if err := applyAccountFlag([]string{"--account=cuenta-3"}); err != nil {
		t.Fatalf("applyAccountFlag: %v", err)
	}
	if got := os.Getenv("PIUMY_ACCOUNT"); got != "cuenta-3" {
		t.Errorf("PIUMY_ACCOUNT = %q, want cuenta-3", got)
	}
}

// The invariant that isn't negotiated: no flag -> nothing changes, neither a
// value the env already had nor the "unset" state.
func TestApplyAccountFlagWithoutFlagLeavesEnvUntouched(t *testing.T) {
	t.Setenv("PIUMY_ACCOUNT", "trabajo")
	if err := applyAccountFlag(nil); err != nil {
		t.Fatalf("applyAccountFlag(nil): %v", err)
	}
	if got := os.Getenv("PIUMY_ACCOUNT"); got != "trabajo" {
		t.Errorf("PIUMY_ACCOUNT = %q, want trabajo (untouched)", got)
	}

	t.Setenv("PIUMY_ACCOUNT", "")
	os.Unsetenv("PIUMY_ACCOUNT")
	if err := applyAccountFlag([]string{}); err != nil {
		t.Fatalf("applyAccountFlag([]): %v", err)
	}
	if v, ok := os.LookupEnv("PIUMY_ACCOUNT"); ok {
		t.Errorf("PIUMY_ACCOUNT was set to %q by a call with no flag — the default install must stay account-less", v)
	}
}

func TestApplyAccountFlagRejectsWhatItDoesNotKnow(t *testing.T) {
	for _, args := range [][]string{
		{"--acount", "cuenta-2"}, // typo: must NOT quietly start the default account
		{"cuenta-2"},             // forgot the flag name
		{"--account", "cuenta-2", "sobra"},
		{"--account"}, // flag without its value
	} {
		t.Setenv("PIUMY_ACCOUNT", "intacta")
		if err := applyAccountFlag(args); err == nil {
			t.Errorf("applyAccountFlag(%v): want an error, got nil", args)
		}
		if got := os.Getenv("PIUMY_ACCOUNT"); got != "intacta" {
			t.Errorf("applyAccountFlag(%v) changed PIUMY_ACCOUNT to %q on error", args, got)
		}
	}
}

func TestOpenDashboardAtStart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account string
		paired  bool
		want    bool
	}{
		{"default install, unlinked: NEVER (invariant)", "", false, false},
		{"default install, linked", "", true, false},
		{"named account, unlinked: opens by itself", "cuenta-2", false, true},
		{"named account, already linked: stays quiet", "cuenta-2", true, false},
	} {
		if got := openDashboardAtStart(tc.account, tc.paired); got != tc.want {
			t.Errorf("%s: openDashboardAtStart(%q, %v) = %v, want %v", tc.name, tc.account, tc.paired, got, tc.want)
		}
	}
}
