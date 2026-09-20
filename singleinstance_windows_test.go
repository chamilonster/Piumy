//go:build windows

package main

import "testing"

// TestDataDirHashSameForDifferentCaseSpellings is S1's core guarantee
// (ct-2026-09-20-1100): two spellings of the SAME Windows directory (which
// is case-insensitive) must collide on one mutex name, or the lock would
// stop doing its job for the exact "same folder, called differently" case
// the contract calls out.
func TestDataDirHashSameForDifferentCaseSpellings(t *testing.T) {
	a := dataDirHash(`C:\Users\boss\AppData\Local\Piumy`)
	b := dataDirHash(`c:\users\BOSS\appdata\local\PIUMY`)
	if a != b {
		t.Errorf("dataDirHash differs by case: %q vs %q, want equal", a, b)
	}
}

// TestDataDirHashDifferentForDifferentDirs is the flip side: two distinct
// accounts (two distinct data directories) must NOT collide, or two live
// Piumy instances could never run at once.
func TestDataDirHashDifferentForDifferentDirs(t *testing.T) {
	a := dataDirHash(`C:\Users\boss\AppData\Local\Piumy\accounts\trabajo`)
	b := dataDirHash(`C:\Users\boss\AppData\Local\Piumy\accounts\personal`)
	if a == b {
		t.Errorf("dataDirHash(trabajo) == dataDirHash(personal) = %q, want different accounts to get different mutexes", a)
	}
}

// TestDataDirHashCollapsesTrailingSeparator: a trailing "\" is the same
// directory as without it — must still hash the same.
func TestDataDirHashCollapsesTrailingSeparator(t *testing.T) {
	a := dataDirHash(`C:\Users\boss\AppData\Local\Piumy`)
	b := dataDirHash(`C:\Users\boss\AppData\Local\Piumy\`)
	if a != b {
		t.Errorf("dataDirHash differs by trailing separator: %q vs %q, want equal", a, b)
	}
}
