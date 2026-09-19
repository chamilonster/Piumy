// Package mediautil: small, vendor-agnostic media helpers shared across
// adapters/tools — first tenant is DecodeDataURL, moved out of
// internal/openwa (ST-E, ct-2026-07-11-1444, before that package was
// deleted) so mcpserver's set_group_icon can decode a data: URL into bytes
// without depending on a specific messaging vendor's package.
package mediautil

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
)

// DecodeDataURL parses "data:<mime>;base64,<payload>" into its raw bytes
// and mime type.
func DecodeDataURL(dataURL string) (data []byte, mime string, err error) {
	const prefix = "data:"
	if !strings.HasPrefix(dataURL, prefix) {
		return nil, "", fmt.Errorf("mediautil: not a data URL")
	}
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 {
		return nil, "", fmt.Errorf("mediautil: malformed data URL, no comma")
	}
	meta := strings.TrimSuffix(dataURL[len(prefix):comma], ";base64")
	payload, err := base64.StdEncoding.DecodeString(dataURL[comma+1:])
	if err != nil {
		return nil, "", err
	}
	return payload, meta, nil
}

// EncodeDataURL is DecodeDataURL's inverse — builds a "data:<mime>;base64,
// <payload>" string from raw bytes. T122 (ct-2026-09-02-2045): the same
// reason send_message accepts a data_url on the way IN (a file path only
// works for a caller on this machine) applies symmetrically on the way
// OUT — get_drafts uses this to hand back a pending draft's saved photo to
// an MCP caller that may not share this machine's filesystem.
func EncodeDataURL(data []byte, mime string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// SaveOutboundMedia writes data into mediaDir under a content-addressed
// filename (its sha256 + ext) and returns the path to read it back later.
// Outbound media has no WhatsApp message id yet at save time — unlike
// inbound media (saved keyed by msgID once a real message exists) — so the
// content hash is the identifier instead; it doubles as free de-dup:
// sending the same bytes twice reuses one file rather than writing it
// again. ext carries the leading dot (".jpg") — kept a parameter, not
// hardcoded to images, so T123's audio can call this same helper with its
// own extension instead of a near-duplicate function.
func SaveOutboundMedia(mediaDir string, data []byte, ext string) (path string, err error) {
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return "", fmt.Errorf("mediautil: mkdir %s: %w", mediaDir, err)
	}
	sum := sha256.Sum256(data)
	path = filepath.Join(mediaDir, hex.EncodeToString(sum[:])+ext)
	if _, statErr := os.Stat(path); statErr == nil {
		return path, nil // same content already saved — reuse, don't rewrite
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("mediautil: write %s: %w", path, err)
	}
	return path, nil
}

// IsOggOpus reports whether data is an OGG container carrying Opus audio —
// WhatsApp's own voice-note format (T123, ct-2026-09-02-2121). Checked by
// the REAL bytes, same principle EnsureJPEG already applies to images
// (never trust a caller-declared mime): every OGG stream starts with the
// literal "OggS" page-header magic, and RFC 7845 requires an Opus
// stream's very first packet to be the "OpusHead" identification header —
// both must be present, or this isn't Opus (it could be an OGG container
// holding Vorbis/FLAC instead, a real and different format). Deliberately
// NOT a decode — no libopus, no CGO, none of the 6 build targets lose
// CGO_ENABLED=0 over this. A format that fails this check is rejected
// outright (mcpserver/send.go), never sent as-is: WhatsApp would show a
// broken/unplayable attachment, read as "Piumy is failing" when the real
// mistake was the caller's format.
func IsOggOpus(data []byte) bool {
	if !bytes.HasPrefix(data, []byte("OggS")) {
		return false
	}
	window := data
	if len(window) > 512 {
		window = window[:512]
	}
	return bytes.Contains(window, []byte("OpusHead"))
}

// waveformBars is how many bars WhatsApp's voice-note waveform draws — the
// same 64 T126's diagnosticWaveformT126 used, now filled from the real
// audio instead of a synthetic pattern (T128, ct-2026-09-03-0133).
const waveformBars = 64

// OpusWaveform approximates a voice note's waveform from data (an OGG/Opus
// file, already validated by IsOggOpus) — T128, ct-2026-09-03-0133.
// Decoding Opus to read real sample energy would need libopus (C), breaking
// CGO_ENABLED=0 on all 6 build targets; that line doesn't move. The
// approximation that doesn't need it: Opus is variable-bitrate, so a
// packet's SIZE is roughly proportional to the audio energy it encodes —
// silence compresses small, loud speech compresses large. Reading a
// packet's byte length only needs the OGG container's own segment table,
// never the encoded audio inside it.
//
// Packets are reconstructed by accumulating segments until one with lacing
// <255 closes them (a >255-byte packet spans several segments, sometimes
// several pages) — reading the first byte of every SEGMENT as if it were
// its own packet is the mistake T126 found in a hand-rolled TOC-byte
// reader; this is the same accumulation done correctly. The first two
// packets (OpusHead, OpusTags, RFC 7845) are container headers, not audio,
// and never contribute a bar.
//
// The N audio-packet sizes are spread across waveformBars contiguous
// buckets (proportional index ranges, not a fixed-size window) and
// averaged, then normalized MIN-MAX: the quietest bar maps to 0, the
// loudest to 100, everything else proportionally between (T128 iteration
// 1 — a lone max-only scale left the drawing flat, since Opus's per-packet
// header overhead means even the quietest real packet never measures 0;
// the floor sat around 55/100 and the whole shape crowded into the top
// half). This one rule covers every packet count without a separate
// branch: N==0 (no audio packets at all — a malformed or empty stream)
// returns all-zero bars before normalization is ever computed, an honest
// flat line rather than fabricated variation. 1<=N<64 spreads the few real
// packets across 64 bars, several bars sharing a nearby packet's value
// rather than any bar being invented — a short clip's waveform just looks
// coarser, never wrong.
//
// min==max (every bucket averaged to the exact same size — a constant
// tone, or N==1 collapsing every bucket onto the same lone packet) would
// divide by zero; there is real, non-empty audio but no variation to
// spread across a range, so it reads as a flat mid-scale line (50) rather
// than NaN, a panic, or the misleading all-100 an earlier version gave a
// single packet.
func OpusWaveform(data []byte) []byte {
	sizes := oggPacketSizes(data)
	if len(sizes) > 2 {
		sizes = sizes[2:] // drop OpusHead + OpusTags — not audio
	} else {
		sizes = nil
	}

	bars := make([]byte, waveformBars)
	n := len(sizes)
	if n == 0 {
		return bars
	}

	raw := make([]float64, waveformBars)
	minV, maxV := 0.0, 0.0
	for i := 0; i < waveformBars; i++ {
		start := i * n / waveformBars
		end := (i + 1) * n / waveformBars
		if end <= start {
			end = start + 1
		}
		if end > n {
			end = n
		}
		sum := 0
		for _, sz := range sizes[start:end] {
			sum += sz
		}
		avg := float64(sum) / float64(end-start)
		raw[i] = avg
		if i == 0 || avg < minV {
			minV = avg
		}
		if avg > maxV {
			maxV = avg
		}
	}

	spread := maxV - minV
	if spread == 0 {
		// Every bar measured the same size — see the doc comment above.
		for i := range bars {
			bars[i] = 50
		}
		return bars
	}
	for i, v := range raw {
		scaled := int((v - minV) / spread * 100)
		switch {
		case scaled > 100:
			scaled = 100
		case scaled < 0:
			scaled = 0
		}
		bars[i] = byte(scaled)
	}
	return bars
}

// oggPacketSizes walks data's OGG pages and returns the byte length of
// every reconstructed packet, in stream order — page payload bytes only
// ever get summed into a running total, never copied, since a waveform
// bar needs a packet's size, not its content. Any structural break (a
// short read, a missing "OggS" where one's expected) stops the walk and
// returns what was gathered so far rather than erroring — a truncated or
// odd file degrades to a shorter/blanker waveform, never a crash on
// something already past IsOggOpus's own check.
func oggPacketSizes(data []byte) []int {
	var sizes []int
	cur := 0
	off := 0
	for off+27 <= len(data) && bytes.Equal(data[off:off+4], []byte("OggS")) {
		nseg := int(data[off+26])
		segTable := off + 27
		if segTable+nseg > len(data) {
			break
		}
		pos := segTable + nseg
		for _, v := range data[segTable : segTable+nseg] {
			if pos+int(v) > len(data) {
				return sizes
			}
			cur += int(v)
			pos += int(v)
			if v < 255 {
				sizes = append(sizes, cur)
				cur = 0
			}
		}
		off = pos
	}
	return sizes
}

// ── T131 (ct-2026-09-03-0621) — normalize the OGG container so WhatsApp
// mobile actually plays the voice note ─────────────────────────────────
//
// Confirmed recipe (Citrino, verified by hand against real files; the
// dueño on his own phone: "Ahora si se escucha!!!"), four things, none
// sufficient alone:
//  1. SILK-only/WB bitstream — CleverCoder's encoder, not this package.
//  2. sample_rate declared 16000 in the OpusHead.
//  3. pre-skip 312.
//  4. OGG pages of ~30 packets (Concentus emits 248; WhatsApp uses 32).
//
// NormalizeOggOpus does 2-4 — the CONTAINER, never the encoded audio
// bytes. No libopus, no decode, go.mod untouched.
const (
	oggTargetPreSkip       = 312
	oggTargetSampleRate    = 16000
	oggAudioPacketsPerPage = 30
	// opusFrameSamples: every packet this pipeline sends or re-sends is a
	// 20ms Opus frame (960 samples @ 48kHz) — the fixed frame size
	// CleverCoder's encoder uses for a voice note. The contract hands this
	// in as an already-settled fact, not something to rediscover by
	// decoding.
	opusFrameSamples = 960
)

// NormalizeOggOpus rewrites data's OGG container to match WhatsApp
// mobile's expectations — the OpusHead's declared sample rate and
// pre-skip, and how many packets share a page — without touching a
// single byte of the encoded audio itself. Never fails: anything that
// doesn't parse as a well-formed, single-logical-stream OGG/Opus file
// (truncated, multiplexed, a header too short to safely rewrite, no
// audio packets at all) returns data completely unchanged — normalizing
// is an improvement this contract asks for, not a requirement a send can
// be blocked on.
//
// Idempotent by construction, not by a special case: the output is
// rebuilt entirely from the real packet stream, so normalizing an
// already-normalized file reproduces the identical bytes (repaging the
// same packets into groups of the same size is deterministic) — an audio
// that already complies never comes out worse.
func NormalizeOggOpus(data []byte) []byte {
	if !IsOggOpus(data) {
		return data
	}
	packets, serial, ok := oggParsedPackets(data)
	if !ok || len(packets) < 2 {
		return data
	}
	head := packets[0]
	if len(head) < 16 || !bytes.HasPrefix(head, []byte("OpusHead")) {
		return data
	}
	audio := packets[2:]
	if len(audio) == 0 {
		return data
	}

	newHead := make([]byte, len(head))
	copy(newHead, head)
	binary.LittleEndian.PutUint16(newHead[10:12], oggTargetPreSkip)
	binary.LittleEndian.PutUint32(newHead[12:16], oggTargetSampleRate)

	var out bytes.Buffer
	out.Write(buildOggPage(0x02, 0, serial, 0, [][]byte{newHead})) // BOS
	out.Write(buildOggPage(0x00, 0, serial, 1, [][]byte{packets[1]}))

	seq := uint32(2)
	granule := int64(0)
	for start := 0; start < len(audio); {
		end := start
		segCount := 0
		for end < len(audio) && end-start < oggAudioPacketsPerPage {
			need := len(audio[end])/255 + 1
			if segCount+need > 255 {
				break
			}
			segCount += need
			end++
		}
		if end == start {
			// A single packet alone needs more than 255 segments —
			// pathological for a 20ms voice frame, and not something this
			// function can safely page. Bail out entirely rather than
			// emit a broken stream.
			return data
		}
		granule += int64(end-start) * opusFrameSamples
		headerType := byte(0)
		if end == len(audio) {
			headerType = 0x04 // EOS
		}
		out.Write(buildOggPage(headerType, granule, serial, seq, audio[start:end]))
		seq++
		start = end
	}
	return out.Bytes()
}

// oggParsedPackets walks data's OGG pages and reconstructs every Opus
// packet's FULL byte content (not just its size, unlike oggPacketSizes'
// waveform-only need) — packets are copied out via append, never
// subslices of data, so a caller can hand them to buildOggPage without
// aliasing the original buffer. ok=false for any structural break: a
// short read, a missing "OggS" where one's expected, more than one
// logical bitstream (a second BOS), or a truncated file that leaves a
// packet unterminated — NormalizeOggOpus treats false as "send
// unchanged", never a partial rebuild. Deliberately a SEPARATE walker
// from oggPacketSizes, not a shared one refactored under it: that
// function's tolerant "return what was gathered so far" is correct for a
// waveform (a coarser bar beats none), but wrong here — this needs an
// all-or-nothing guarantee, since a partial page rebuild would silently
// corrupt the stream in a way "leave unchanged" never does.
func oggParsedPackets(data []byte) (packets [][]byte, serial uint32, ok bool) {
	off := 0
	haveBOS := false
	var cur []byte
	for off < len(data) {
		if off+27 > len(data) || !bytes.Equal(data[off:off+4], []byte("OggS")) {
			return nil, 0, false
		}
		headerType := data[off+5]
		pageSerial := binary.LittleEndian.Uint32(data[off+14 : off+18])
		nseg := int(data[off+26])
		segTableStart := off + 27
		if segTableStart+nseg > len(data) {
			return nil, 0, false
		}
		segTable := data[segTableStart : segTableStart+nseg]
		pos := segTableStart + nseg

		if headerType&0x02 != 0 { // BOS
			if haveBOS {
				return nil, 0, false // a second logical stream — not handled
			}
			haveBOS = true
			serial = pageSerial
		} else if !haveBOS || pageSerial != serial {
			return nil, 0, false // page before BOS, or a multiplexed stream
		}

		for _, lace := range segTable {
			if pos+int(lace) > len(data) {
				return nil, 0, false
			}
			cur = append(cur, data[pos:pos+int(lace)]...)
			pos += int(lace)
			if lace < 255 {
				packets = append(packets, cur)
				cur = nil
			}
		}
		off = pos
	}
	if !haveBOS || len(cur) > 0 {
		return nil, 0, false // no stream at all, or EOF mid-packet
	}
	return packets, serial, true
}

// buildOggPage assembles one OGG page from a set of complete packets —
// every packet given here gets its own full lacing sequence, closing
// within THIS page; this function never splits one across pages — and
// stamps its CRC32 last, over the whole page with the checksum field
// zeroed, per the OGG spec.
func buildOggPage(headerType byte, granule int64, serial, sequence uint32, pagePackets [][]byte) []byte {
	var segTable []byte
	var payload []byte
	for _, pkt := range pagePackets {
		n := len(pkt)
		for n >= 255 {
			segTable = append(segTable, 255)
			n -= 255
		}
		segTable = append(segTable, byte(n))
		payload = append(payload, pkt...)
	}
	page := make([]byte, 27+len(segTable)+len(payload))
	copy(page[0:4], "OggS")
	page[5] = headerType
	binary.LittleEndian.PutUint64(page[6:14], uint64(granule))
	binary.LittleEndian.PutUint32(page[14:18], serial)
	binary.LittleEndian.PutUint32(page[18:22], sequence)
	page[26] = byte(len(segTable))
	copy(page[27:27+len(segTable)], segTable)
	copy(page[27+len(segTable):], payload)
	binary.LittleEndian.PutUint32(page[22:26], oggCRC32(page))
	return page
}

// oggCRC32Table/oggCRC32 implement the OGG page checksum exactly as the
// spec defines it (xiph.org's framing spec): polynomial 0x04c11db7,
// initial value 0, NOT reflected, no final XOR — a different algorithm
// from the far more common reflected CRC-32 (zlib/PNG/gzip), despite
// sharing the same generator polynomial constant. Recalculated (not
// copied verbatim) is the whole point of this contract: skipping it
// leaves the page corrupt with the identical symptom the rest of this
// contract fixes.
var oggCRC32Table = func() [256]uint32 {
	var table [256]uint32
	for i := range table {
		crc := uint32(i) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
		table[i] = crc
	}
	return table
}()

func oggCRC32(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ oggCRC32Table[byte(crc>>24)^b]
	}
	return crc
}

// validWaveformBytes reports whether w is a well-formed 64-bar 0-100
// waveform — shared by WaveformFromInts (an MCP caller's own measurement)
// and LoadWaveformSidecar (re-checking a sidecar file before trusting it),
// so "what counts as valid" is decided in exactly one place.
func validWaveformBytes(w []byte) bool {
	if len(w) != waveformBars {
		return false
	}
	for _, v := range w {
		if v > 100 {
			return false
		}
	}
	return true
}

// WaveformFromInts converts send_message's optional audio_waveform param
// (T128 iteration 2, ct-2026-09-03-0133) — CleverCoder's own measurement
// of the ORIGINAL uncompressed audio, better than OpusWaveform's guess
// from the already-compressed Opus, when it's supplied — into the []byte
// shape AudioMessage.Waveform needs. ok=false for anything not exactly 64
// values each 0-100: this is an external, untrusted value headed straight
// into a WhatsApp message field, so a malformed one is never used, never
// fails the send — the caller falls back to OpusWaveform instead.
func WaveformFromInts(vals []int) ([]byte, bool) {
	if len(vals) != waveformBars {
		return nil, false
	}
	out := make([]byte, waveformBars)
	for i, v := range vals {
		if v < 0 || v > 100 {
			return nil, false
		}
		out[i] = byte(v)
	}
	return out, true
}

// waveformSidecarPath is where an externally-measured waveform for
// audioData lives — keyed by the SAME sha256 SaveOutboundMedia already
// names the audio file with (T128 iteration 2), so the audio's own content
// hash is the join key between the two files. No DB column, no outbox
// plumbing: SendAudio already has the audio bytes in hand to recompute
// this same path and look the sidecar up on its own.
func waveformSidecarPath(mediaDir string, audioData []byte) string {
	sum := sha256.Sum256(audioData)
	return filepath.Join(mediaDir, hex.EncodeToString(sum[:])+".waveform")
}

// SaveWaveformSidecar writes waveform (already validated by the caller —
// WaveformFromInts or equivalent) next to audioData's own saved file.
func SaveWaveformSidecar(mediaDir string, audioData, waveform []byte) error {
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return fmt.Errorf("mediautil: mkdir %s: %w", mediaDir, err)
	}
	path := waveformSidecarPath(mediaDir, audioData)
	if err := os.WriteFile(path, waveform, 0o644); err != nil {
		return fmt.Errorf("mediautil: write %s: %w", path, err)
	}
	return nil
}

// LoadWaveformSidecar looks up a waveform SaveWaveformSidecar saved for
// audioData, re-validating it before returning — a sidecar file is as
// external an input as the MCP param that created it, never trusted
// blind. ok=false for anything missing, unreadable, or malformed; the
// caller (SendAudio) falls back to OpusWaveform(audioData) either way —
// same "degrade, never break" rule the param itself follows.
func LoadWaveformSidecar(mediaDir string, audioData []byte) ([]byte, bool) {
	data, err := os.ReadFile(waveformSidecarPath(mediaDir, audioData))
	if err != nil || !validWaveformBytes(data) {
		return nil, false
	}
	return data, true
}

// EnsureJPEG (T111, ct-2026-09-01-1442 — the boss: "que lo convierta a
// jpg") returns data unchanged if it's already a JPEG — decodeing a lossy
// format and re-encoding it would only degrade it for no reason — or
// converts any other decodable image (PNG, GIF, ...) to JPEG. Whether it's
// "already a JPEG" is decided by the REAL format image.Decode detects, not
// a caller-supplied mime string — a mismatched mime must never skip the
// conversion a genuinely non-JPEG payload needs.
//
// A PNG's transparency has no JPEG equivalent (no alpha channel) — the
// transparent area is composited over a SOLID WHITE background before
// encoding, never left to decode as black/garbage. White, not the
// dashboard's own theme color: this photo is seen on WhatsApp, by every
// contact of the account, not on piumy's tablero — the two have nothing to
// do with each other, and white is simply the conventional choice for a
// profile photo (Citrino, T111).
//
// A payload that can't be decoded as any registered image format returns
// an error naming that — this only handles the common image formats, not
// arbitrary bytes.
func EnsureJPEG(data []byte) ([]byte, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mediautil: could not decode as an image: %w", err)
	}
	if format == "jpeg" {
		return data, nil
	}
	opaque := image.NewRGBA(img.Bounds())
	draw.Draw(opaque, opaque.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(opaque, opaque.Bounds(), img, img.Bounds().Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, opaque, nil); err != nil {
		return nil, fmt.Errorf("mediautil: could not encode as jpeg: %w", err)
	}
	return buf.Bytes(), nil
}
