package config

import "strings"

// AccountLabel is the name a named account shows to the person (S5,
// ct-2026-09-23-2038, Citrino's format): "<WhatsApp name> · ...<last 4 digits
// of its own number>" once linked ("Camilo · ...7132"), just "...7132" when the
// session has no name, and the account id ("cuenta-2") until it has a number.
// The digits are what make it unique per account with no need to look at the
// others: two accounts can share a WhatsApp name (personal and business of the
// same person), never a number.
//
// Empty account -> "" — the default account never shows a label, however named
// its WhatsApp is (S1's "no account, no change" rule). Tray, window title,
// dashboard and shortcut names all read this one function; the COLOR keeps
// coming from the id (ColorForAccount), so a rename never repaints the account.
func AccountLabel(account, ownName, ownJID string) string {
	if account == "" {
		return ""
	}
	var parts []string
	if ownName != "" {
		parts = append(parts, ownName)
	}
	if tail := numberTail(ownJID); tail != "" {
		parts = append(parts, "..."+tail)
	}
	if len(parts) == 0 {
		return account
	}
	return strings.Join(parts, " · ")
}

// numberTail is the last 4 digits of a JID's user part ("" when it has none).
// The device suffix ("number:12@...") is cut first, or its digits would be
// read as the number's.
func numberTail(jid string) string {
	user, _, _ := strings.Cut(jid, "@")
	user, _, _ = strings.Cut(user, ":")
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, user)
	if len(digits) > 4 {
		digits = digits[len(digits)-4:]
	}
	return digits
}
