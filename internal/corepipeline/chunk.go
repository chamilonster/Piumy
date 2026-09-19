// T101 (ct-2026-08-29-1651): splitting a long outbound reply into
// WhatsApp-sized pieces — boss verbatim: "partirla en pedazos pero que no
// se envien como metralla, pequeño delay". No person writes a single
// 6000-character bubble; a whole reply arriving as one giant balloon is
// visible to the contact as exactly what it is: a bot.
package corepipeline

import "strings"

// splitIntoChunks divides text into pieces no longer than maxLen, cutting
// by PARAGRAPH first (a blank line, "\n\n") and packing consecutive short
// paragraphs together up to the limit. Only a single paragraph that alone
// exceeds maxLen falls back to hardCut. text within maxLen (the common
// case — most replies) or maxLen<=0 returns text as its own single-element
// slice, unchanged — the caller uses len(result)==1 to skip every bit of
// chunking machinery (no delay, no extra bookkeeping) for the ordinary
// short reply.
func splitIntoChunks(text string, maxLen int) []string {
	if maxLen <= 0 || len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
	}

	for _, unit := range paragraphUnits(text) {
		if len(unit) > maxLen {
			flush()
			if strings.Count(unit, "```") >= 2 {
				// Contains a complete code fence — hardCut knows nothing
				// about fences and would gladly cut through one; ship the
				// whole unit intact, even past maxLen, rather than break
				// the code block (same "longer beats broken" rule).
				chunks = append(chunks, unit)
			} else {
				chunks = append(chunks, hardCut(unit, maxLen)...)
			}
			continue
		}
		grown := current.Len()
		if grown > 0 {
			grown += len("\n\n")
		}
		grown += len(unit)
		if grown > maxLen {
			flush()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(unit)
	}
	flush()

	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

// paragraphUnits splits on blank lines like a plain paragraph split would,
// EXCEPT it re-joins any run of paragraphs that together open and close a
// ``` code fence — a blank line INSIDE a fenced code block must never be
// treated as a paragraph boundary, or splitIntoChunks could sever the fence
// itself across two WhatsApp messages.
func paragraphUnits(text string) []string {
	raw := strings.Split(text, "\n\n")
	var units []string
	var pending []string
	fenceOpen := false
	for _, p := range raw {
		pending = append(pending, p)
		if strings.Count(p, "```")%2 == 1 {
			fenceOpen = !fenceOpen
		}
		if !fenceOpen {
			units = append(units, strings.Join(pending, "\n\n"))
			pending = nil
		}
	}
	if len(pending) > 0 {
		units = append(units, strings.Join(pending, "\n\n"))
	}
	return units
}

// hardCut splits one too-long paragraph at a line break or whitespace
// boundary near maxLen — never mid-word, mid-URL, or mid-token. When no
// break exists within the limit (one huge unbroken token, e.g. a long
// link), it looks FORWARD for the next break instead of forcing a cut —
// the contract's own rule: "ante la duda, un pedazo más largo antes que
// romper algo." A token with no break anywhere survives whole, however
// long, rather than being torn apart.
func hardCut(text string, maxLen int) []string {
	var out []string
	for len(text) > maxLen {
		window := text[:maxLen]
		cut := strings.LastIndexByte(window, '\n')
		if cut <= 0 {
			cut = strings.LastIndexByte(window, ' ')
		}
		if cut <= 0 {
			next := strings.IndexAny(text, " \n")
			if next < 0 {
				break // nothing to break on anywhere — keep it whole
			}
			cut = next
		}
		out = append(out, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}
