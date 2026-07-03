// SPDX-License-Identifier: AGPL-3.0-only
package sessionbackup

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSnapshotSQLiteProducesValidReadableCopy covers the DoD directly: the
// VACUUM INTO snapshot is a valid SQLite file with the same data as the
// source, taken without needing to stop whatever else has the source open
// (WAL mode).
func TestSnapshotSQLiteProducesValidReadableCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	dst := filepath.Join(dir, "dst.db")

	srcDB, err := sql.Open("sqlite", "file:"+src+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer srcDB.Close()
	if _, err := srcDB.Exec("CREATE TABLE t(x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := srcDB.Exec("INSERT INTO t VALUES (1),(2),(3)"); err != nil {
		t.Fatal(err)
	}

	// Source connection stays OPEN (WAL, not stopped) while snapshotting —
	// this is the whole point of VACUUM INTO for a hot backup.
	if err := snapshotSQLite(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}

	dstDB, err := sql.Open("sqlite", "file:"+dst)
	if err != nil {
		t.Fatal(err)
	}
	defer dstDB.Close()
	var count int
	if err := dstDB.QueryRow("SELECT COUNT(*) FROM t").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("snapshot row count = %d, want 3", count)
	}
}
