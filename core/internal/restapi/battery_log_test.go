// SPDX-License-Identifier: AGPL-3.0-only
package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestParseBatteryLogLine covers the row format written by adapters/power/
// timeremain.py's _append_log: ts,voltage_mv,
// raw_pct,linearized_pct,charging — including the empty-voltage case (no
// reading yet) and malformed lines (a power-cut-truncated trailing row).
func TestParseBatteryLogLine(t *testing.T) {
	row, ok := parseBatteryLogLine("1700000000,3950,72,71.5,0")
	if !ok {
		t.Fatal("expected a valid row to parse")
	}
	if row.TS != 1700000000 || row.VoltageMV == nil || *row.VoltageMV != 3950 ||
		row.RawPct != 72 || row.LinearizedPct != 71.5 || row.Charging {
		t.Fatalf("parsed row mismatch: %+v", row)
	}

	if row, ok := parseBatteryLogLine("1700000000,,72,71.5,1"); !ok || row.VoltageMV != nil || !row.Charging {
		t.Fatalf("empty voltage_mv should parse as nil, charging=1 as true: %+v ok=%v", row, ok)
	}

	for _, bad := range []string{
		"",
		"1700000000,3950,72,71.5", // too few columns (truncated mid-write)
		"not-a-ts,3950,72,71.5,0",
		"1700000000,not-a-volt,72,71.5,0",
	} {
		if _, ok := parseBatteryLogLine(bad); ok {
			t.Fatalf("expected malformed line to be rejected: %q", bad)
		}
	}
}

// TestReadBatteryLog covers the DoD directly: header is skipped, a
// malformed trailing line (the real failure mode from a power cut mid-
// write) is dropped rather than erroring the whole read, and `limit`
// returns only the most recent rows.
func TestReadBatteryLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "battery_log.csv")
	content := "ts,voltage_mv,raw_pct,linearized_pct,charging\n" +
		"100,4100,90,90.0,0\n" +
		"160,4050,85,84.0,0\n" +
		"220,4000,80,78.5,0\n" +
		"280,3950,75,72" // truncated last line, no trailing newline
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rows, err := readBatteryLog(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 valid rows (header + truncated tail skipped), got %d: %+v", len(rows), rows)
	}
	if rows[0].TS != 100 || rows[2].TS != 220 {
		t.Fatalf("unexpected row order/content: %+v", rows)
	}

	limited, err := readBatteryLog(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].TS != 160 || limited[1].TS != 220 {
		t.Fatalf("limit=2 should keep the LAST 2 rows: %+v", limited)
	}

	// Missing file: empty slice, not an error (fresh install / log disabled).
	rows, err = readBatteryLog(filepath.Join(t.TempDir(), "does-not-exist.csv"), 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing file should return empty/no-error, got rows=%v err=%v", rows, err)
	}
}

// TestBatteryLogEndpoint covers the DoD directly: GET /api/battery/log
// serves the configured CSV as JSON, and an unconfigured BatteryLogFile
// degrades to an empty array rather than 503/error.
func TestBatteryLogEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "battery_log.csv")
	content := "ts,voltage_mv,raw_pct,linearized_pct,charging\n100,4100,90,90.0,0\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(Deps{BatteryLogFile: path})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/battery/log", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var rows []batteryLogRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TS != 100 {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}

	h = Handler(Deps{}) // BatteryLogFile unset
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/battery/log", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("unconfigured BatteryLogFile: status=%d body=%q, want 200 []", rec.Code, rec.Body.String())
	}
}
