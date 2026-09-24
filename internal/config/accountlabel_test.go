package config

import "testing"

func TestAccountLabel(t *testing.T) {
	for _, tc := range []struct {
		name, account, ownName, ownJID, want string
	}{
		{"default account never shows a label, even linked", "", "Contacto Uno", "55500000041@s.whatsapp.net", ""},
		{"default account, nothing", "", "", "", ""},
		{"named account before linking shows its id", "cuenta-2", "", "", "cuenta-2"},
		{"linked, with a name", "cuenta-2", "Contacto Uno", "55500000041@s.whatsapp.net", "Contacto Uno · ...0041"},
		{"linked, no name: just the digits", "cuenta-2", "", "55500000041@s.whatsapp.net", "...0041"},
		{"the device suffix is not part of the number", "cuenta-2", "Contacto Uno", "55500000041:12@s.whatsapp.net", "Contacto Uno · ...0041"},
		{"a short number keeps what it has", "cuenta-2", "", "555@s.whatsapp.net", "...555"},
		{"a name without a number yet", "cuenta-2", "Contacto Uno", "", "Contacto Uno"},
	} {
		if got := AccountLabel(tc.account, tc.ownName, tc.ownJID); got != tc.want {
			t.Errorf("%s: AccountLabel(%q, %q, %q) = %q, want %q", tc.name, tc.account, tc.ownName, tc.ownJID, got, tc.want)
		}
	}
}

// Citrino's own case: two accounts with the SAME WhatsApp name (personal and
// business of one person) must not read the same in the tray or the window.
func TestAccountLabelDiffersForTwoAccountsWithTheSameName(t *testing.T) {
	a := AccountLabel("cuenta-2", "Contacto Uno", "55500000041@s.whatsapp.net")
	b := AccountLabel("cuenta-3", "Contacto Uno", "55500000042@s.whatsapp.net")
	if a == b {
		t.Errorf("two accounts with one WhatsApp name got the same label %q", a)
	}
}
