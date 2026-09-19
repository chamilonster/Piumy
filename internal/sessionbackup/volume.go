package sessionbackup

// WarnIfSameVolume logs a warning (via logf, e.g. log.Printf) if backupDir
// and sessionDir live on the same filesystem/volume — best-effort, only a
// reminder: the default backup location defends against DB corruption, NOT
// against the SD card itself dying. Real disaster recovery needs an off-SD
// backup path (deploy-time wiring, out of scope here). Skips silently —
// never warns falsely — when the check itself can't be done (unsupported
// platform, missing path).
func WarnIfSameVolume(backupDir, sessionDir string, logf func(format string, args ...any)) {
	same, ok := sameVolume(backupDir, sessionDir)
	if !ok || !same {
		return
	}
	logf("sessionbackup: WARNING — backup dir %q is on the SAME volume as the store DB (%q). This protects against DB corruption, NOT against SD card failure. Real disaster recovery needs an off-SD backup path.", backupDir, sessionDir)
}
