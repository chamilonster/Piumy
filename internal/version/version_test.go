package version

import (
	"os"
	"strings"
	"testing"
)

// TestVersionMatchesRootFile guards the single-source-of-truth promise: the
// version this package reports (via the embedded fallback, since `go test`
// never passes -ldflags -X) must equal VERSION at the repo root, not just
// the local embedded copy — catching the two files drifting apart, which is
// exactly the bug that motivated this package (server.go and piumy.iss each
// had their own hand-typed, mismatched literal).
func TestVersionMatchesRootFile(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty — the embedded fallback in init() didn't run")
	}
	root, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatalf("reading repo-root VERSION: %v", err)
	}
	want := strings.TrimSpace(string(root))
	if Version != want {
		t.Errorf("Version = %q, want %q (repo-root VERSION) — internal/version/VERSION is a stale copy, re-run build-all.sh", Version, want)
	}
}
