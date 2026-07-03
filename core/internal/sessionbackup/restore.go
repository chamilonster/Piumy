// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
package sessionbackup

import (
	"fmt"
	"os"
)

// Restore decrypts backupPath with passphrase and atomically replaces
// sessionDBPath with the result. MANUAL/deliberate only — the
// caller (main.go's restore-session subcommand) is expected to have already
// called CheckNotServing; Restore itself does NOT check that again, so it
// stays usable in tests without needing a fake lock file.
func Restore(backupPath, sessionDBPath, passphrase string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("sessionbackup: read backup file: %w", err)
	}
	plain, err := decryptBytes(data, []byte(passphrase))
	if err != nil {
		return err
	}
	defer zeroBytes(plain)

	if err := atomicWriteFile(sessionDBPath, plain, 0o600); err != nil {
		return fmt.Errorf("sessionbackup: write session db: %w", err)
	}
	return nil
}
