package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// legacyDataHint pairs one pre-T169 bare relative default name (what Load()
// used to fall back to before ct-2026-09-19-1433) with the Config field
// that replaced it. Closed and historical on purpose: every data field
// this package gains AFTER T169 defaults through DataDir from birth, so it
// can never have one of these — new fields are never added to this list.
type legacyDataHint struct {
	legacyName string // checked against the CURRENT WORKING DIRECTORY
	newPath    string // this run's actual resolved location for the same data
}

// WarnLegacyData reports one line per pre-T169 relative-default name that
// still exists in the CURRENT WORKING DIRECTORY while this run's own
// resolved location for that same data doesn't exist yet (T169,
// ct-2026-09-19-1433) — the exact risk the contract calls out: real data
// sitting right here, about to be silently passed over in favor of a
// brand-new empty file/folder somewhere else, as if it had been lost.
//
// Never migrates anything, never decides for the operator — naming what it
// found and where this run is actually looking is the whole job. Call
// AFTER config.Load() (so cfg's paths are already resolved) and log
// whatever comes back; an empty slice means nothing to warn about.
func WarnLegacyData(cfg Config) []string {
	hints := []legacyDataHint{
		{"piumy.db", cfg.DBPath},
		{"whatsmeow.db", cfg.WADBPath},
		{"router.json", cfg.RouterPath},
		{"status.json", cfg.StatusPath},
		{"media", cfg.MediaDir},
	}
	var warnings []string
	for _, h := range hints {
		if _, err := os.Stat(h.legacyName); err != nil {
			continue // nothing at the old relative name — nothing to warn about
		}
		if _, err := os.Stat(h.newPath); err == nil {
			continue // the new location already has data too — not the silent-loss case
		}
		cwd, _ := os.Getwd()
		warnings = append(warnings, fmt.Sprintf(
			"%q existe en el directorio actual (%s) pero este arranque está mirando en %s y ahí todavía no hay nada — no se movió ni se tocó nada; si esos son datos reales, copiá el archivo a la ubicación nueva o apuntá la variable de entorno correspondiente ahí",
			filepath.Join(cwd, h.legacyName), cwd, h.newPath))
	}
	return warnings
}
