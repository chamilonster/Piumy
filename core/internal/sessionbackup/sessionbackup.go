// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package sessionbackup: encrypted, rotated backups of the WhatsApp session
// SQLite DB (entregable A — SENSITIVE, touches credentials).
// Snapshot via VACUUM INTO (WAL-safe, no need to stop the gateway) →
// AES-256-GCM encrypt with a scrypt-derived key from PIMYWA_BACKUP_KEY →
// atomic write into a rotated directory. No key configured → backups are
// DISABLED (never write the session unencrypted, full stop) — same
// fail-safe-off pattern as bridge.NoneBridge / mcpserver.RequireBearerToken
// with an empty key. Restore is a SEPARATE, deliberate operation (CLI
// subcommand, not REST — see main.go) that refuses unless the service is
// confirmed stopped (see lock.go).
package sessionbackup

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config configures a Backuper. SessionDBPath and Dir are always required;
// Key empty disables backups (fail-safe-off, see package doc).
type Config struct {
	SessionDBPath string
	Key           string
	Dir           string
	Keep          int           // newest N kept on rotation; <=0 defaults to 5
	Interval      time.Duration // periodic ticker cadence; <=0 defaults to 24h
}

// Result is one backup's outcome, returned by BackupNow and reported via
// Status — metadata only, never anything secret.
type Result struct {
	Path      string    `json:"path,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

// Status is the current backup state, as shown on the dashboard / GET
// /api/backup.
type Status struct {
	Enabled    bool   `json:"enabled"`
	LastBackup Result `json:"last_backup"` // zero value if none yet
	Count      int    `json:"count"`       // how many backups currently retained
}

// Backuper runs backups on demand and on a schedule. The zero value is NOT
// usable — construct with New.
type Backuper struct {
	sessionDBPath string
	key           []byte // nil when disabled
	dir           string
	keep          int
	interval      time.Duration

	mu   sync.Mutex
	last Result
}

// New builds a Backuper from cfg. An empty cfg.Key disables backups —
// BackupNow becomes a no-op — and logs a startup warning (the session must
// never be written to disk unencrypted).
func New(cfg Config) *Backuper {
	b := &Backuper{
		sessionDBPath: cfg.SessionDBPath,
		dir:           cfg.Dir,
		keep:          cfg.Keep,
		interval:      cfg.Interval,
	}
	if b.keep <= 0 {
		b.keep = 5
	}
	if b.interval <= 0 {
		b.interval = 24 * time.Hour
	}
	if cfg.Key == "" {
		log.Println("sessionbackup: WARNING — backups DISABLED (PIMYWA_BACKUP_KEY not set); the WhatsApp session is never written to disk unencrypted")
		return b
	}
	b.key = []byte(cfg.Key)
	return b
}

// Enabled reports whether a backup key is configured.
func (b *Backuper) Enabled() bool {
	return b.key != nil
}

// Status returns the current state for the dashboard/REST — metadata only.
func (b *Backuper) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	names, _ := b.listBackups() // best-effort; a listing error just shows 0
	return Status{Enabled: b.Enabled(), LastBackup: b.last, Count: len(names)}
}

// BackupNow takes a snapshot, encrypts it, writes it into Dir, and rotates
// old backups. No-op (returns the zero Result, nil error) if disabled.
func (b *Backuper) BackupNow(ctx context.Context) (Result, error) {
	if !b.Enabled() {
		return Result{}, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("sessionbackup: create backup dir: %w", err)
	}

	tmpSnapshot := filepath.Join(b.dir, fmt.Sprintf(".snapshot-%d.tmp", time.Now().UnixNano()))
	defer os.Remove(tmpSnapshot) // best-effort cleanup; the encrypted copy is what matters
	if err := snapshotSQLite(ctx, b.sessionDBPath, tmpSnapshot); err != nil {
		return Result{}, err
	}

	plain, err := os.ReadFile(tmpSnapshot)
	if err != nil {
		return Result{}, fmt.Errorf("sessionbackup: read snapshot: %w", err)
	}
	defer zeroBytes(plain)

	encrypted, err := encryptBytes(plain, b.key)
	if err != nil {
		return Result{}, err
	}

	now := time.Now()
	// Nanosecond precision (not Unix seconds) so two backups started within
	// the same second — "backup ahora" hit twice quickly, say — never
	// collide on the filename and silently overwrite one another.
	finalPath := filepath.Join(b.dir, fmt.Sprintf("session-%d.bak", now.UnixNano()))
	if err := atomicWriteFile(finalPath, encrypted, 0o600); err != nil {
		return Result{}, fmt.Errorf("sessionbackup: write backup: %w", err)
	}

	if err := b.rotate(); err != nil {
		log.Printf("sessionbackup: rotate: %v", err)
	}

	res := Result{Path: finalPath, SizeBytes: int64(len(encrypted)), At: now}
	b.last = res
	log.Printf("sessionbackup: backup written (%s, %d bytes)", finalPath, res.SizeBytes)
	return res, nil
}

// RunPeriodic ticks BackupNow on Interval until ctx is cancelled. No-op loop
// (still runs, just never does anything) when disabled, so callers don't
// need to special-case wiring it.
func (b *Backuper) RunPeriodic(ctx context.Context) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := b.BackupNow(ctx); err != nil {
				log.Printf("sessionbackup: periodic backup: %v", err)
			}
		}
	}
}

// BackupIfDue runs BackupNow only if minInterval has elapsed since the last
// backup (or none has run yet) — a debounce for event-triggered call sites
// (the post-link hook) that could otherwise fire repeatedly in a short
// window (e.g. flaky wifi reconnecting over and over) and waste a Pi Zero's
// CPU on scrypt derivations for no benefit.
func (b *Backuper) BackupIfDue(ctx context.Context, minInterval time.Duration) {
	if !b.Enabled() {
		return
	}
	b.mu.Lock()
	last := b.last.At
	b.mu.Unlock()
	if !last.IsZero() && time.Since(last) < minInterval {
		return
	}
	if _, err := b.BackupNow(ctx); err != nil {
		log.Printf("sessionbackup: post-link backup: %v", err)
	}
}

// listBackups returns backup file paths in b.dir, oldest first. Caller must
// hold b.mu.
func (b *Backuper) listBackups() ([]string, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "session-") && strings.HasSuffix(e.Name(), ".bak") {
			names = append(names, e.Name())
		}
	}
	// Names sort lexicographically the same as by their embedded UnixNano
	// timestamp (fixed "session-<digits>.bak" shape, same digit count for
	// centuries — no padding needed) — oldest first.
	sort.Strings(names)
	return names, nil
}

// rotate deletes the oldest backups beyond b.keep. Caller must hold b.mu.
func (b *Backuper) rotate() error {
	names, err := b.listBackups()
	if err != nil {
		return err
	}
	if len(names) <= b.keep {
		return nil
	}
	for _, name := range names[:len(names)-b.keep] {
		if err := os.Remove(filepath.Join(b.dir, name)); err != nil {
			log.Printf("sessionbackup: rotate: remove %s: %v", name, err)
		}
	}
	return nil
}

// atomicWriteFile writes data to a temp file in the same directory as path,
// then renames it into place — the write is atomic from any reader's POV
// (matches the tmp+rename pattern already used for status.json — 3rd
// commandment: a power cut mid-write must never leave a half-written,
// corrupt backup masquerading as a complete one).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
