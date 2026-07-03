// SPDX-License-Identifier: AGPL-3.0-only
package sessionbackup

import (
	"bytes"
	"testing"
)

// TestEncryptDecryptRoundTrip covers the DoD directly: encrypt then decrypt
// with the same passphrase returns the original bytes exactly.
func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte("this is a fake WhatsApp session sqlite file, pretend")
	passphrase := []byte("correct horse battery staple")

	encrypted, err := encryptBytes(plain, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encrypted, plain) {
		t.Fatal("encrypted output equals plaintext — encryption didn't happen")
	}

	decrypted, err := decryptBytes(encrypted, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("decrypted = %q, want %q", decrypted, plain)
	}
}

// TestDecryptWrongPassphraseFailsCleanly covers the DoD's error-handling
// requirement: a wrong passphrase produces a generic error, never a panic,
// never any hint about WHY it failed (wrong passphrase vs corruption look
// identical from the outside — deliberately).
func TestDecryptWrongPassphraseFailsCleanly(t *testing.T) {
	encrypted, err := encryptBytes([]byte("secret session data"), []byte("right passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = decryptBytes(encrypted, []byte("wrong passphrase"))
	if err == nil {
		t.Fatal("decrypt with wrong passphrase succeeded, want an error")
	}
}

// TestEncryptUsesFreshSaltAndNonceEveryCall covers the DoD's "never a fixed
// salt" requirement: encrypting the SAME plaintext twice produces different
// ciphertexts (and different embedded salt/nonce prefixes).
func TestEncryptUsesFreshSaltAndNonceEveryCall(t *testing.T) {
	plain := []byte("same plaintext both times")
	passphrase := []byte("same passphrase both times")

	a, err := encryptBytes(plain, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	b, err := encryptBytes(plain, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical output — salt/nonce are not being randomized")
	}
	if bytes.Equal(a[:saltLen], b[:saltLen]) {
		t.Error("salt was reused across two separate encrypt calls")
	}
}

// TestDecryptRejectsTruncatedData covers a corrupted/truncated backup file
// failing cleanly instead of panicking (index out of range).
func TestDecryptRejectsTruncatedData(t *testing.T) {
	if _, err := decryptBytes([]byte("too short"), []byte("whatever")); err == nil {
		t.Error("decrypt of truncated data succeeded, want an error")
	}
	if _, err := decryptBytes(nil, []byte("whatever")); err == nil {
		t.Error("decrypt of nil data succeeded, want an error")
	}
}
