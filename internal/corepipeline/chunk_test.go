// T101 (ct-2026-08-29-1651): splitIntoChunks is the pure text-splitting
// logic — tested standalone, no store/gateway/governor involved, so the
// cutting rules (paragraph-first, hard-cut fallback, never mid-token, never
// inside a code fence) are cheap and fast to pin down before wiring them
// into processOutbox.
package corepipeline

import (
	"strings"
	"testing"
)

func TestSplitIntoChunksShortTextIsOneChunk(t *testing.T) {
	text := "hola, ¿cómo estás?"
	got := splitIntoChunks(text, 4000)
	if len(got) != 1 || got[0] != text {
		t.Fatalf("splitIntoChunks(short) = %+v, want a single unchanged chunk", got)
	}
}

func TestSplitIntoChunksExactlyAtLimitIsOneChunk(t *testing.T) {
	text := strings.Repeat("a", 100)
	got := splitIntoChunks(text, 100)
	if len(got) != 1 || got[0] != text {
		t.Fatalf("splitIntoChunks(text at exactly maxLen) = %+v, want a single unchanged chunk", got)
	}
}

func TestSplitIntoChunksPacksParagraphsUpToLimit(t *testing.T) {
	// Each paragraph is well under maxLen=30; the two short ones should
	// pack together, the third (which would overflow the pack) starts a
	// new chunk.
	text := "primero corto\n\nsegundo corto\n\ntercero que ya no entra en el mismo pedazo"
	got := splitIntoChunks(text, 30)
	if len(got) < 2 {
		t.Fatalf("splitIntoChunks(multi-paragraph over limit) = %+v, want at least 2 chunks", got)
	}
	// No chunk should be empty, and rejoining must reconstruct real content
	// (no paragraph silently dropped).
	rejoined := strings.Join(got, "\n\n")
	for _, want := range []string{"primero corto", "segundo corto", "tercero que ya no entra"} {
		if !strings.Contains(rejoined, want) {
			t.Errorf("rejoined chunks = %q, missing %q — content lost", rejoined, want)
		}
	}
}

func TestSplitIntoChunksSingleLongParagraphHardCuts(t *testing.T) {
	// One paragraph, no blank lines, well over maxLen — must hard-cut, not
	// just refuse to split.
	words := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		words = append(words, "palabra")
	}
	text := strings.Join(words, " ") // "palabra palabra ... " — 50*8-1 = 399 chars
	got := splitIntoChunks(text, 100)
	if len(got) < 2 {
		t.Fatalf("splitIntoChunks(single long paragraph) = %d chunk(s), want a hard cut into several", len(got))
	}
	for i, c := range got {
		if len(c) > 100 {
			t.Errorf("chunk %d length=%d, want <= 100 (words have spaces to cut on)", i, len(c))
		}
	}
	rejoined := strings.Join(got, " ")
	if strings.Count(rejoined, "palabra") != 50 {
		t.Errorf("rejoined = %q, want all 50 words preserved, not merged or dropped", rejoined)
	}
}

func TestSplitIntoChunksNeverCutsMidWord(t *testing.T) {
	got := splitIntoChunks(strings.Repeat("palabra ", 20), 25)
	for _, c := range got {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "abra") || strings.HasSuffix(trimmed, "pal") {
			t.Errorf("chunk %q looks cut mid-word", c)
		}
	}
}

func TestSplitIntoChunksUnbreakableTokenStaysWhole(t *testing.T) {
	// A single token with no spaces at all (e.g. a long URL) longer than
	// maxLen must never be torn apart — "ante la duda, un pedazo más largo
	// antes que romper algo" (the contract's own rule).
	url := "https://example.com/" + strings.Repeat("x", 200)
	got := splitIntoChunks(url, 50)
	found := false
	for _, c := range got {
		if c == url {
			found = true
		}
	}
	if !found {
		t.Errorf("splitIntoChunks(unbreakable token) = %+v, want the token to survive whole in one chunk", got)
	}
}

func TestSplitIntoChunksDoesNotSplitInsideCodeFence(t *testing.T) {
	code := "```go\nfunc main() {\n\nfmt.Println(\"hola\")\n\n}\n```"
	text := "antes del código\n\n" + code + "\n\ndespués del código"
	got := splitIntoChunks(text, 20)
	rejoined := strings.Join(got, "\n\n")
	if !strings.Contains(rejoined, code) {
		t.Errorf("code fence not preserved intact across chunks: rejoined=%q", rejoined)
	}
	// Stronger check: whichever chunk contains the opening fence must also
	// contain the closing one.
	for _, c := range got {
		if strings.Contains(c, "func main()") {
			if !strings.Contains(c, "```go") || strings.Count(c, "```") != 2 {
				t.Errorf("code fence split across chunks — chunk with the code body = %q", c)
			}
		}
	}
}

func TestSplitIntoChunksZeroOrNegativeMaxLenNoSplit(t *testing.T) {
	text := strings.Repeat("a", 5000)
	got := splitIntoChunks(text, 0)
	if len(got) != 1 || got[0] != text {
		t.Errorf("splitIntoChunks(maxLen<=0) = %+v, want no splitting at all", got)
	}
}
