// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// File format (entregable A): each backup is SELF-CONTAINED, so any
// single file — moved anywhere — restores with just the passphrase, no other
// state to keep track of:
//
//	[16 bytes salt][12 bytes GCM nonce][ciphertext + 16-byte GCM tag]
//
// A fresh random salt AND nonce are generated on every encrypt — never a
// fixed/shared salt (that would weaken scrypt against precomputation across
// backups/installs).
package sessionbackup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

const (
	saltLen = 16
	// nonceLen is the standard GCM nonce size cipher.NewGCM uses by default
	// (crypto/cipher doesn't export this as a named constant, but it's the
	// well-known fixed value for the non-custom-nonce-size constructor).
	nonceLen = 12
	// scrypt cost parameters (Colin Percival's original "interactive" values):
	// N=2^15, r=8, p=1 — ~32 MB of RAM, a few hundred ms on weak hardware
	// (Pi Zero 2 W). Backups run at most a few times a day; this is not a
	// login-latency-sensitive path, so these are not tuned down further.
	scryptN      = 32768
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32 // AES-256
)

// deriveKey derives a 32-byte AES-256 key from passphrase and salt via
// scrypt. Never logged, never included in error messages — callers must
// scrub the returned key with zeroBytes once done with it.
func deriveKey(passphrase, salt []byte) ([]byte, error) {
	key, err := scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		// scrypt's own errors are parameter-validation only (never include
		// input bytes), safe to wrap as-is.
		return nil, fmt.Errorf("sessionbackup: derive key: %w", err)
	}
	return key, nil
}

// encryptBytes seals plain under a key derived from passphrase + a freshly
// generated salt/nonce, returning the self-contained
// salt||nonce||ciphertext format described above.
func encryptBytes(plain, passphrase []byte) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("sessionbackup: generate salt: %w", err)
	}
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("sessionbackup: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sessionbackup: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("sessionbackup: generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plain, nil)

	out := make([]byte, 0, len(salt)+len(nonce)+len(ciphertext))
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// decryptBytes reverses encryptBytes. Errors are DELIBERATELY generic (wrong
// passphrase and file corruption look the same from the outside) — never
// echo the passphrase or any derived key material.
func decryptBytes(data, passphrase []byte) ([]byte, error) {
	if len(data) < saltLen+nonceLen {
		return nil, fmt.Errorf("sessionbackup: file too short to be a valid backup")
	}
	salt := data[:saltLen]
	nonce := data[saltLen : saltLen+nonceLen]
	ciphertext := data[saltLen+nonceLen:]

	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("sessionbackup: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sessionbackup: gcm: %w", err)
	}

	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("sessionbackup: decrypt failed — wrong passphrase or corrupted backup")
	}
	return plain, nil
}

// zeroBytes overwrites b with zeros — best-effort defense in depth, NOT a
// cryptographic guarantee: Go's GC can have copied these bytes elsewhere
// (escape analysis, slice growth) before this runs. Real secret-memory
// hygiene would need mlock/a hardened allocator, out of scope here. This
// still costs nothing and shrinks the window a secret sits in the heap.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
