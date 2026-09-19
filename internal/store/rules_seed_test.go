package store

import "testing"

// TestSeedFactoryRulesIfUnsetFreshInstall covers T13's item 1: a clean
// install (nothing in kv yet) gets all 4 factory values (T79,
// ct-2026-08-27-2034, dropped the 5th — the global default).
func TestSeedFactoryRulesIfUnsetFreshInstall(t *testing.T) {
	s := openTestStore(t)

	seeded, err := s.SeedFactoryRulesIfUnset()
	if err != nil {
		t.Fatal(err)
	}
	if len(seeded) != 4 {
		t.Errorf("keysSeeded = %v, want all 4 factory keys", seeded)
	}

	checks := []struct {
		key, want string
	}{
		{SettingIdentity, FactoryIdentity},
		{SettingRulesTypeGroup, FactoryRulesTypeGroup},
		{SettingRulesDefaultContact, FactoryRulesDefaultContact},
		{SettingRulesDefaultNewNumber, FactoryRulesDefaultNewNumber},
	}
	for _, c := range checks {
		got, err := s.KVGet(c.key)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("KVGet(%s) = %q, want the factory default", c.key, got)
		}
	}
}

// TestSeedFactoryRulesIfUnsetSkipsWrittenField covers T13's item 3: a field
// the owner already wrote (any value, including an explicit empty string —
// "lo dejaron vacío a propósito") must never be touched.
func TestSeedFactoryRulesIfUnsetSkipsWrittenField(t *testing.T) {
	s := openTestStore(t)

	if err := s.KVSet(SettingRulesTypeGroup, "mi regla propia"); err != nil {
		t.Fatal(err)
	}
	if err := s.KVSet(SettingIdentity, ""); err != nil {
		t.Fatal(err) // deliberately emptied, not "never written"
	}

	seeded, err := s.SeedFactoryRulesIfUnset()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range seeded {
		if k == SettingRulesTypeGroup || k == SettingIdentity {
			t.Errorf("keysSeeded = %v, must not include an already-written key", seeded)
		}
	}

	got, err := s.KVGet(SettingRulesTypeGroup)
	if err != nil || got != "mi regla propia" {
		t.Errorf("KVGet(rules_type_group) = %q, err=%v — must survive untouched", got, err)
	}
	identity, err := s.KVGet(SettingIdentity)
	if err != nil || identity != "" {
		t.Errorf("KVGet(identity) = %q, err=%v — a deliberate empty must stay empty, not get reseeded", identity, err)
	}

	// The 2 untouched keys still get their factory default.
	other, err := s.KVGet(SettingRulesDefaultContact)
	if err != nil || other != FactoryRulesDefaultContact {
		t.Errorf("KVGet(rules_default_contact) = %q, err=%v, want the factory default (never written)", other, err)
	}
}

// TestSeedFactoryRulesIfUnsetIdempotent: running it twice never reseeds
// what the first run already wrote.
func TestSeedFactoryRulesIfUnsetIdempotent(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.SeedFactoryRulesIfUnset(); err != nil {
		t.Fatal(err)
	}
	seeded, err := s.SeedFactoryRulesIfUnset()
	if err != nil {
		t.Fatal(err)
	}
	if len(seeded) != 0 {
		t.Errorf("second run keysSeeded = %v, want none — everything is already written", seeded)
	}
}

// TestKVExistsDistinguishesNeverWrittenFromExplicitEmpty is the mechanism
// SeedFactoryRulesIfUnset relies on — KVGet alone can't tell these apart
// (both return "").
func TestKVExistsDistinguishesNeverWrittenFromExplicitEmpty(t *testing.T) {
	s := openTestStore(t)

	exists, err := s.KVExists("never_written_key")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("KVExists on a key never written = true, want false")
	}

	if err := s.KVSet("explicit_empty_key", ""); err != nil {
		t.Fatal(err)
	}
	exists, err = s.KVExists("explicit_empty_key")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("KVExists on a key explicitly set to '' = false, want true — it WAS written")
	}
}
