package sessionbackup

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// snapshotSQLite takes a WAL-safe, consistent snapshot of the SQLite
// database at sourcePath into a NEW file at destPath (which must not already
// exist), via SQLite's own VACUUM INTO — hot-backup-safe by design, no need
// to stop whatever else has sourcePath open. Opens its own short-lived,
// read-only connection — this package never touches store/corepipeline
// internals. Verified empirically against modernc.org/sqlite: both mode=ro
// and a bound (?) destination parameter for VACUUM INTO work as expected.
func snapshotSQLite(ctx context.Context, sourcePath, destPath string) error {
	dsn := "file:" + sourcePath + "?_pragma=busy_timeout(5000)&mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sessionbackup: open session db: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", destPath); err != nil {
		return fmt.Errorf("sessionbackup: vacuum into: %w", err)
	}
	return nil
}
