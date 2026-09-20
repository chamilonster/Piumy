package mediautil

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeDataURL(t *testing.T) {
	raw, mime, err := DecodeDataURL("data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("hola")))
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || string(raw) != "hola" {
		t.Errorf("got mime=%q raw=%q, want image/png / hola", mime, raw)
	}
	if _, _, err := DecodeDataURL("not-a-data-url"); err == nil {
		t.Error("DecodeDataURL on garbage input: want an error")
	}
	if _, _, err := DecodeDataURL("data:image/pngbase64nocomma"); err == nil {
		t.Error("DecodeDataURL with a prefix but no comma: want an error")
	}
	if _, _, err := DecodeDataURL("data:image/png;base64,not-valid-base64!!"); err == nil {
		t.Error("DecodeDataURL with invalid base64 payload: want an error")
	}
}

// ── T122 (ct-2026-09-02-2045) — EncodeDataURL / SaveOutboundMedia ──────────

// TestEncodeDataURLRoundTripsWithDecode: EncodeDataURL's own output must
// parse back through DecodeDataURL to the same bytes/mime — the two exist
// as a pair (inbound decode, outbound encode) and must stay each other's
// exact inverse.
func TestEncodeDataURLRoundTripsWithDecode(t *testing.T) {
	want := []byte("una foto cualquiera")
	url := EncodeDataURL(want, "image/jpeg")
	data, mime, err := DecodeDataURL(url)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/jpeg" || !bytes.Equal(data, want) {
		t.Errorf("round trip = mime=%q data=%q, want image/jpeg / %q", mime, data, want)
	}
}

// TestSaveOutboundMediaWritesReadableFile: the returned path must actually
// contain the bytes given.
func TestSaveOutboundMediaWritesReadableFile(t *testing.T) {
	dir := t.TempDir()
	data := []byte("bytes de una foto")
	path, err := SaveOutboundMedia(dir, data, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("path = %q, want it under %q", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back %s: %v", path, err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("file content = %q, want %q", got, data)
	}
}

// TestSaveOutboundMediaDedupsIdenticalContent: the same bytes saved twice
// must resolve to the same path (content-addressed) instead of writing a
// second copy.
func TestSaveOutboundMediaDedupsIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	data := []byte("misma foto, dos envios")
	path1, err := SaveOutboundMedia(dir, data, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	path2, err := SaveOutboundMedia(dir, data, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	if path1 != path2 {
		t.Errorf("paths for identical content differ: %q vs %q", path1, path2)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("files in dir after saving identical content twice = %d, want 1", len(entries))
	}
}

// TestSaveOutboundMediaCreatesMissingDir: mediaDir must be created if it
// doesn't exist yet, same as inbound's saveMedia (whatsmeow/media.go).
func TestSaveOutboundMediaCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "media")
	if _, err := SaveOutboundMedia(dir, []byte("x"), ".jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("mediaDir was not created: %v", err)
	}
}

// ── T123 (ct-2026-09-02-2121) — IsOggOpus ───────────────────────────────

// fakeOggOpus builds a synthetic-but-magic-byte-correct Ogg/Opus payload:
// the real validator (IsOggOpus) only sniffs magic bytes, never decodes
// audio, so a real encoder isn't needed to test it — same reasoning
// EnsureJPEG's own tests use real stdlib-encoded images because THAT
// function really decodes, while this one deliberately doesn't.
func fakeOggOpus() []byte {
	return []byte("OggS" + string(make([]byte, 23)) + "OpusHead" + "resto del payload, no importa el contenido real")
}

func TestIsOggOpusAcceptsRealMagicBytes(t *testing.T) {
	if !IsOggOpus(fakeOggOpus()) {
		t.Error("IsOggOpus on a well-formed OggS+OpusHead payload = false, want true")
	}
}

func TestIsOggOpusRejectsPlainGarbage(t *testing.T) {
	if IsOggOpus([]byte("esto no es audio de ningun tipo")) {
		t.Error("IsOggOpus on garbage = true, want false")
	}
}

// TestIsOggOpusRejectsOggWithoutOpus: an OGG container that ISN'T Opus
// (e.g. Vorbis/FLAC inside Ogg) must be rejected — checking only "OggS"
// would wrongly accept those too.
func TestIsOggOpusRejectsOggWithoutOpus(t *testing.T) {
	notOpus := []byte("OggS" + string(make([]byte, 23)) + "VorbisHead" + "resto")
	if IsOggOpus(notOpus) {
		t.Error("IsOggOpus on an Ogg/Vorbis payload (no OpusHead) = true, want false")
	}
}

// TestIsOggOpusRejectsOpusHeadWithoutOggContainer: "OpusHead" appearing
// somewhere in random bytes, with no real Ogg page header in front of it,
// must not pass — checking only "OpusHead" would wrongly accept that too.
func TestIsOggOpusRejectsOpusHeadWithoutOggContainer(t *testing.T) {
	if IsOggOpus([]byte("bytes cualquiera que mencionan OpusHead en el medio, sin ser Ogg real")) {
		t.Error("IsOggOpus without a leading OggS magic = true, want false")
	}
}

// TestIsOggOpusRejectsWAV is the contract's own explicit trap: talk(save_to=...)
// gives a plain uncompressed .wav — RIFF/WAVE header, not Ogg at all — and
// that must be rejected, not silently accepted as "close enough".
func TestIsOggOpusRejectsWAV(t *testing.T) {
	wavHeader := []byte("RIFF" + string(make([]byte, 4)) + "WAVEfmt ")
	if IsOggOpus(wavHeader) {
		t.Error("IsOggOpus on a WAV header = true, want false — talk(save_to=...) output must be rejected, not sent as-is")
	}
}

// ── T111 (ct-2026-09-01-1442) — EnsureJPEG ──────────────────────────────────

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func solidImage(c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// decodeFormat is EnsureJPEG's own oracle — the real, detected format, not
// a trusted mime string.
func decodeFormat(t *testing.T, data []byte) string {
	t.Helper()
	_, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding EnsureJPEG's own output: %v", err)
	}
	return format
}

// TestEnsureJPEGLeavesJPEGUnchanged: a JPEG that's already a JPEG must not
// be re-encoded — re-compressing a lossy format degrades it for no reason.
func TestEnsureJPEGLeavesJPEGUnchanged(t *testing.T) {
	src := encodeJPEG(t, solidImage(color.RGBA{200, 50, 50, 255}))
	got, err := EnsureJPEG(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Error("EnsureJPEG re-encoded an already-JPEG image — want the exact same bytes back, untouched")
	}
}

// TestEnsureJPEGConvertsPNG: a PNG comes back as a real, decodable JPEG.
func TestEnsureJPEGConvertsPNG(t *testing.T) {
	src := encodePNG(t, solidImage(color.RGBA{10, 200, 10, 255}))
	got, err := EnsureJPEG(src)
	if err != nil {
		t.Fatal(err)
	}
	if format := decodeFormat(t, got); format != "jpeg" {
		t.Errorf("format after EnsureJPEG(png) = %q, want jpeg", format)
	}
}

// TestEnsureJPEGConvertsGIF: same conversion for a GIF.
func TestEnsureJPEGConvertsGIF(t *testing.T) {
	src := encodeGIF(t, solidImage(color.RGBA{10, 10, 200, 255}))
	got, err := EnsureJPEG(src)
	if err != nil {
		t.Fatal(err)
	}
	if format := decodeFormat(t, got); format != "jpeg" {
		t.Errorf("format after EnsureJPEG(gif) = %q, want jpeg", format)
	}
}

// TestEnsureJPEGCompositesTransparencyOverWhite: JPEG has no alpha channel
// — a fully transparent PNG pixel must come back close to white, not black
// or garbage (Citrino, T111: "es lo convencional para una foto de perfil").
func TestEnsureJPEGCompositesTransparencyOverWhite(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	// Transparent black — if compositing did nothing (left it black instead
	// of blending white underneath), this would come back dark, not white.
	img.Set(0, 0, color.RGBA{0, 0, 0, 0})
	img.Set(1, 0, color.RGBA{0, 0, 0, 0})
	img.Set(0, 1, color.RGBA{0, 0, 0, 0})
	img.Set(1, 1, color.RGBA{0, 0, 0, 0})
	src := encodePNG(t, img)

	got, err := EnsureJPEG(src)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	// JPEG is lossy — allow some slack instead of demanding exact 255s.
	const whiteFloor = 240 << 8
	if r < whiteFloor || g < whiteFloor || b < whiteFloor {
		t.Errorf("transparent pixel came back r=%d g=%d b=%d (16-bit), want all close to white (>= %d)", r, g, b, whiteFloor)
	}
}

// TestEnsureJPEGRejectsUndecodableData: garbage input (not an image at all)
// must error with something that explains what happened, not panic.
func TestEnsureJPEGRejectsUndecodableData(t *testing.T) {
	_, err := EnsureJPEG([]byte("esto no es una imagen"))
	if err == nil {
		t.Fatal("EnsureJPEG on non-image bytes: want an error")
	}
}

// ── T128 (ct-2026-09-03-0133) — OpusWaveform ────────────────────────────────

// buildOggPages is OpusWaveform's own test oracle: a structurally real OGG
// container (correct page headers + segment/lacing tables) with exactly the
// packet sizes requested, in order — no CRC (oggPacketSizes never checks
// it) and no real Opus payload bytes (only their COUNT is ever read).
// packetSizes[0] and [1] stand in for OpusHead/OpusTags — every case below
// prepends two header-sized packets before the "real" audio sizes under
// test, same as a genuine file always carries them.
func buildOggPages(t *testing.T, packetSizes []int) []byte {
	t.Helper()
	var buf bytes.Buffer
	for seq, size := range packetSizes {
		var segs []byte
		remaining := size
		for remaining >= 255 {
			segs = append(segs, 255)
			remaining -= 255
		}
		segs = append(segs, byte(remaining))

		buf.WriteString("OggS")
		buf.WriteByte(0)                                              // version
		buf.WriteByte(0)                                              // header_type (BOS/EOS unused by the parser)
		buf.Write(make([]byte, 8))                                    // granule position, unused
		must(t, binary.Write(&buf, binary.LittleEndian, uint32(1)))   // serial
		must(t, binary.Write(&buf, binary.LittleEndian, uint32(seq))) // sequence
		must(t, binary.Write(&buf, binary.LittleEndian, uint32(0)))   // crc, unchecked
		buf.WriteByte(byte(len(segs)))
		buf.Write(segs)
		buf.Write(make([]byte, size)) // payload content — irrelevant, only length matters
	}
	return buf.Bytes()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// withHeaders prepends two fixed-size stand-in packets for OpusHead/
// OpusTags, matching how a real file always starts (RFC 7845) — OpusWaveform
// always drops the first two reconstructed packets as container headers.
func withHeaders(audioSizes ...int) []int {
	return append([]int{19, 31}, audioSizes...)
}

// TestOggPacketSizesReconstructsMultiSegmentPackets is DoD item 2, directly:
// a packet over 255 bytes spans more than one lacing segment, and only the
// FULL reconstructed packet's length is a real size — reading each
// segment's own byte count as if it were its own packet (the mistake T126
// found measuring TOC bytes) would report 255 and 45 here instead of 300.
func TestOggPacketSizesReconstructsMultiSegmentPackets(t *testing.T) {
	sizes := []int{10, 10, 300, 50, 255, 5}
	data := buildOggPages(t, sizes)

	got := oggPacketSizes(data)
	if len(got) != len(sizes) {
		t.Fatalf("oggPacketSizes = %v, want %v", got, sizes)
	}
	for i, want := range sizes {
		if got[i] != want {
			t.Errorf("packet %d size = %d, want %d", i, got[i], want)
		}
	}
}

// TestOpusWaveformDeterministicAndDistinct is DoD item 3: the same audio
// bytes always produce the same waveform (no hidden randomness/time
// dependence), and two audios with genuinely different packet-size shapes
// produce different waveforms (it isn't a constant regardless of input).
func TestOpusWaveformDeterministicAndDistinct(t *testing.T) {
	loud := buildOggPages(t, withHeaders(50, 100, 150, 200, 150, 100, 50))
	quiet := buildOggPages(t, withHeaders(5, 5, 5, 5, 5, 5, 5))

	w1 := OpusWaveform(loud)
	w2 := OpusWaveform(loud)
	if !bytes.Equal(w1, w2) {
		t.Errorf("same audio produced different waveforms:\n%v\n%v", w1, w2)
	}

	w3 := OpusWaveform(quiet)
	if bytes.Equal(w1, w3) {
		t.Errorf("a loud and a quiet audio produced the same waveform: %v", w1)
	}
}

// TestOpusWaveformLowBarsForLeadingSilence is DoD item 4: a lead-in of tiny
// (silent) packets has to read as low bars near the start, well below the
// loud packets that follow — the check that the approximation actually
// tracks the sound instead of being noise.
func TestOpusWaveformLowBarsForLeadingSilence(t *testing.T) {
	var sizes []int
	for i := 0; i < 64; i++ {
		sizes = append(sizes, 4) // silence: tiny packets
	}
	for i := 0; i < 64; i++ {
		sizes = append(sizes, 200) // speech: large packets
	}
	data := buildOggPages(t, withHeaders(sizes...))

	w := OpusWaveform(data)
	if w[0] > 20 {
		t.Errorf("first bar = %d, want low (near-silent lead-in)", w[0])
	}
	if w[len(w)-1] < 80 {
		t.Errorf("last bar = %d, want high (loud tail)", w[len(w)-1])
	}
}

// TestOpusWaveformUsesFullRangeEvenWithoutASilentPacket is T128 iteration 1:
// a max-only scale left the drawing flat, because Opus's per-packet header
// overhead means even the quietest real packet never measures 0 bytes — the
// floor sat around 55/100 in the dueño's real audio and the whole shape
// crowded into the top half. min-max normalization has to use the FULL
// 0-100 range regardless of where the real floor sits: the quietest bar
// reads 0, the loudest reads 100, even though no packet in this audio is
// anywhere near byte-size 0.
func TestOpusWaveformUsesFullRangeEvenWithoutASilentPacket(t *testing.T) {
	// Every packet size is >= 150 (nothing close to 0) but they still vary
	// quiet-to-loud — a stand-in for the real "floor sits at 55/100" bug.
	sizes := []int{150, 160, 180, 220, 260, 300, 260, 220, 180, 160}
	data := buildOggPages(t, withHeaders(sizes...))

	w := OpusWaveform(data)
	hasZero, hasHundred := false, false
	for _, v := range w {
		if v == 0 {
			hasZero = true
		}
		if v == 100 {
			hasHundred = true
		}
	}
	if !hasZero {
		t.Errorf("waveform = %v, want at least one bar at 0 (the quietest packet, min-max floor)", w)
	}
	if !hasHundred {
		t.Errorf("waveform = %v, want at least one bar at 100 (the loudest packet, min-max ceiling)", w)
	}
}

// TestOpusWaveformFlatWhenEveryPacketMatchesSize is the new edge case
// min-max introduces: if every bucket averages to the exact same size
// (max-min == 0), dividing by the spread would panic/NaN. There IS real,
// non-empty audio (unlike the N==0 case, which returns early as all-zero
// before this is even computed) — just no variation to spread across a
// range — so it has to degrade to a flat, explicit mid-scale line (50),
// never crash.
func TestOpusWaveformFlatWhenEveryPacketMatchesSize(t *testing.T) {
	sizes := make([]int, 80)
	for i := range sizes {
		sizes[i] = 42 // a constant tone: every packet the same size
	}
	data := buildOggPages(t, withHeaders(sizes...))

	w := OpusWaveform(data)
	if len(w) != waveformBars {
		t.Fatalf("len(waveform) = %d, want %d", len(w), waveformBars)
	}
	for i, v := range w {
		if v != 50 {
			t.Errorf("bar %d = %d, want 50 (flat, no variation to show)", i, v)
		}
	}
}

// TestOpusWaveformDegenerateCasesStayInRange is DoD item 5: an empty
// stream, one with only the two header packets (no audio at all), a single
// audio packet, and fewer than waveformBars audio packets must all return
// exactly waveformBars values in [0,100] — never panic, never a short
// slice, never an out-of-range byte.
func TestOpusWaveformDegenerateCasesStayInRange(t *testing.T) {
	fewSizes := make([]int, 5)
	for i := range fewSizes {
		fewSizes[i] = 10 * (i + 1)
	}

	cases := []struct {
		name  string
		sizes []int
	}{
		{"archivo vacio", nil},
		{"solo cabeceras, sin audio", withHeaders()},
		{"un solo paquete de audio", withHeaders(77)},
		{"menos de 64 paquetes de audio", withHeaders(fewSizes...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildOggPages(t, c.sizes)
			w := OpusWaveform(data)
			if len(w) != waveformBars {
				t.Fatalf("len(waveform) = %d, want %d", len(w), waveformBars)
			}
			// w is []byte, always >=0 — only the upper bound needs checking.
			for i, v := range w {
				if v > 100 {
					t.Errorf("bar %d = %d, want <= 100", i, v)
				}
			}
		})
	}
}

// ── T128 iteration 2 (ct-2026-09-03-0133) — WaveformFromInts / sidecar ─────

func validWaveformInts() []int {
	vals := make([]int, waveformBars)
	for i := range vals {
		vals[i] = i % 101 // varied, all in 0-100
	}
	return vals
}

// TestWaveformFromIntsAcceptsAWellFormedWaveform: the golden path — exactly
// 64 values, each 0-100, converts cleanly and keeps every value.
func TestWaveformFromIntsAcceptsAWellFormedWaveform(t *testing.T) {
	vals := validWaveformInts()
	got, ok := WaveformFromInts(vals)
	if !ok {
		t.Fatal("WaveformFromInts on a well-formed 64-value waveform: want ok=true")
	}
	if len(got) != waveformBars {
		t.Fatalf("len = %d, want %d", len(got), waveformBars)
	}
	for i, v := range vals {
		if int(got[i]) != v {
			t.Errorf("bar %d = %d, want %d", i, got[i], v)
		}
	}
}

// TestWaveformFromIntsRejectsMalformedInput is DoD-equivalent for iteration
// 2's own validation requirement: wrong length, an out-of-range value (both
// directions), and empty all have to come back ok=false — never a panic,
// never a silently-clamped value.
func TestWaveformFromIntsRejectsMalformedInput(t *testing.T) {
	tooShort := validWaveformInts()[:63]
	tooLong := append(validWaveformInts(), 50)
	tooHigh := validWaveformInts()
	tooHigh[10] = 101
	negative := validWaveformInts()
	negative[10] = -1

	cases := []struct {
		name string
		vals []int
	}{
		{"vacio", nil},
		{"muy corto (63)", tooShort},
		{"muy largo (65)", tooLong},
		{"valor > 100", tooHigh},
		{"valor negativo", negative},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := WaveformFromInts(c.vals)
			if ok {
				t.Errorf("WaveformFromInts(%v) = %v, ok=true — want ok=false", c.vals, got)
			}
			if got != nil {
				t.Errorf("WaveformFromInts on malformed input returned %v, want nil", got)
			}
		})
	}
}

// TestWaveformSidecarRoundTrips: SaveWaveformSidecar followed by
// LoadWaveformSidecar on the SAME audio bytes returns exactly what was
// saved — this is the whole join mechanism (content hash as the key), so
// the round trip itself is the thing that has to hold.
func TestWaveformSidecarRoundTrips(t *testing.T) {
	dir := t.TempDir()
	audio := []byte("bytes de audio, el contenido real no importa aca")
	want, _ := WaveformFromInts(validWaveformInts())

	if err := SaveWaveformSidecar(dir, audio, want); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadWaveformSidecar(dir, audio)
	if !ok {
		t.Fatal("LoadWaveformSidecar after a successful Save: want ok=true")
	}
	if !bytes.Equal(got, want) {
		t.Errorf("LoadWaveformSidecar = %v, want %v", got, want)
	}
}

// TestWaveformSidecarMissingFallsBackCleanly: SendAudio's whole reason for
// checking ok — no sidecar was ever saved for this audio (the common case,
// audio_waveform wasn't given) has to report ok=false, not an error, not a
// zero-value slice mistaken for real data.
func TestWaveformSidecarMissingFallsBackCleanly(t *testing.T) {
	dir := t.TempDir()
	got, ok := LoadWaveformSidecar(dir, []byte("nunca se guardo un sidecar para esto"))
	if ok {
		t.Errorf("LoadWaveformSidecar with nothing saved = %v, ok=true — want ok=false", got)
	}
	if got != nil {
		t.Errorf("LoadWaveformSidecar with nothing saved returned %v, want nil", got)
	}
}

// TestWaveformSidecarCorruptedOnDiskIsRejected: defense in depth — even a
// sidecar file that DOES exist gets re-validated before use, same rule as
// any other external input. A stale/corrupted file must fall back exactly
// like a missing one, never get used as-is.
func TestWaveformSidecarCorruptedOnDiskIsRejected(t *testing.T) {
	dir := t.TempDir()
	audio := []byte("audio cuyo sidecar en disco esta corrupto")
	path := waveformSidecarPath(dir, audio)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0o644); err != nil { // wrong length
		t.Fatal(err)
	}

	got, ok := LoadWaveformSidecar(dir, audio)
	if ok {
		t.Errorf("LoadWaveformSidecar on a corrupted sidecar = %v, ok=true — want ok=false", got)
	}
}

// ── T131 (ct-2026-09-03-0621) — NormalizeOggOpus ────────────────────────

// writeTestOggPage writes one raw OGG page, hand-rolled — deliberately
// NOT built via mediautil.go's own buildOggPage, so a bug shared between
// a fixture builder and the function under test can't hide from these
// tests. CRC is always written as 0 — nothing under test here ever
// checks an INPUT page's checksum (oggParsedPackets doesn't, matching
// oggPacketSizes's own established convention above); only
// NormalizeOggOpus's OUTPUT checksum is ever verified, independently
// (see independentOggCRC32).
func writeTestOggPage(t *testing.T, buf *bytes.Buffer, headerType byte, granule int64, serial, sequence uint32, packets [][]byte) {
	t.Helper()
	var segTable, payload []byte
	for _, pkt := range packets {
		n := len(pkt)
		for n >= 255 {
			segTable = append(segTable, 255)
			n -= 255
		}
		segTable = append(segTable, byte(n))
		payload = append(payload, pkt...)
	}
	buf.WriteString("OggS")
	buf.WriteByte(0) // version
	buf.WriteByte(headerType)
	must(t, binary.Write(buf, binary.LittleEndian, uint64(granule)))
	must(t, binary.Write(buf, binary.LittleEndian, serial))
	must(t, binary.Write(buf, binary.LittleEndian, sequence))
	must(t, binary.Write(buf, binary.LittleEndian, uint32(0))) // crc, unchecked on the way in
	buf.WriteByte(byte(len(segTable)))
	buf.Write(segTable)
	buf.Write(payload)
}

// buildOggOpusFile assembles a synthetic OGG/Opus stream — a real
// OpusHead carrying the given preSkip/sampleRate (the fields
// NormalizeOggOpus rewrites), a stand-in OpusTags packet, and
// audioPackets grouped packetsPerPage-per-page — the same shape a real
// upstream encoder produces (Concentus: 248/page) or an already-good one
// (WhatsApp's own ~32/page).
func buildOggOpusFile(t *testing.T, preSkip uint16, sampleRate uint32, audioPackets [][]byte, packetsPerPage int) []byte {
	t.Helper()
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1 // version
	head[9] = 1 // channel count
	binary.LittleEndian.PutUint16(head[10:12], preSkip)
	binary.LittleEndian.PutUint32(head[12:16], sampleRate)
	tags := []byte("OpusTagsstandin")

	var buf bytes.Buffer
	writeTestOggPage(t, &buf, 0x02, 0, 4242, 0, [][]byte{head})
	writeTestOggPage(t, &buf, 0x00, 0, 4242, 1, [][]byte{tags})

	seq := uint32(2)
	granule := int64(0)
	for start := 0; start < len(audioPackets); start += packetsPerPage {
		end := start + packetsPerPage
		if end > len(audioPackets) {
			end = len(audioPackets)
		}
		granule += int64(end-start) * opusFrameSamples
		headerType := byte(0)
		if end == len(audioPackets) {
			headerType = 0x04
		}
		writeTestOggPage(t, &buf, headerType, granule, 4242, seq, audioPackets[start:end])
		seq++
	}
	return buf.Bytes()
}

// fakeAudioPackets returns n small, distinct packets — content doesn't
// matter to NormalizeOggOpus (it never decodes audio), only that each
// packet's exact bytes survive the round trip.
func fakeAudioPackets(n int) [][]byte {
	packets := make([][]byte, n)
	for i := range packets {
		packets[i] = []byte{byte(i), byte(i >> 8), byte(i >> 16)}
	}
	return packets
}

// independentOggCRC32 is a SECOND implementation of the OGG page
// checksum — bit-by-bit, no lookup table — deliberately sharing no code
// with mediautil.go's own oggCRC32Table/oggCRC32, so a test asserting
// CRC correctness verifies the algorithm itself, not just that the
// function agrees with its own table.
func independentOggCRC32(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

type testOggPage struct {
	headerType        byte
	granule           int64
	serial, sequence  uint32
	segTable, payload []byte
	storedCRC         uint32
}

func (p testOggPage) audioPacketCount() int {
	n := 0
	for _, v := range p.segTable {
		if v < 255 {
			n++
		}
	}
	return n
}

// parseOggPagesForTest is a THIRD, independent low-level OGG page reader
// used only to inspect NormalizeOggOpus's OUTPUT (page count, header
// flags, per-page checksum) — never to build input. Fails the test
// outright on any structural surprise: this checks known-good output,
// it doesn't need oggParsedPackets's own tolerance for a real-world
// truncated file.
func parseOggPagesForTest(t *testing.T, data []byte) []testOggPage {
	t.Helper()
	var pages []testOggPage
	off := 0
	for off < len(data) {
		if off+27 > len(data) || string(data[off:off+4]) != "OggS" {
			t.Fatalf("parseOggPagesForTest: bad page at offset %d (len %d)", off, len(data))
		}
		headerType := data[off+5]
		granule := int64(binary.LittleEndian.Uint64(data[off+6 : off+14]))
		serial := binary.LittleEndian.Uint32(data[off+14 : off+18])
		sequence := binary.LittleEndian.Uint32(data[off+18 : off+22])
		storedCRC := binary.LittleEndian.Uint32(data[off+22 : off+26])
		nseg := int(data[off+26])
		segTable := append([]byte(nil), data[off+27:off+27+nseg]...)
		payloadLen := 0
		for _, v := range segTable {
			payloadLen += int(v)
		}
		payloadStart := off + 27 + nseg
		payload := append([]byte(nil), data[payloadStart:payloadStart+payloadLen]...)
		pages = append(pages, testOggPage{headerType, granule, serial, sequence, segTable, payload, storedCRC})
		off = payloadStart + payloadLen
	}
	return pages
}

// verifyPageCRC rebuilds p's own bytes (checksum field zeroed) from its
// already-parsed fields and checks independentOggCRC32 agrees with the
// stored value — the contract's own DoD item 2.
func verifyPageCRC(t *testing.T, p testOggPage) {
	t.Helper()
	buf := make([]byte, 27+len(p.segTable)+len(p.payload))
	copy(buf[0:4], "OggS")
	buf[5] = p.headerType
	binary.LittleEndian.PutUint64(buf[6:14], uint64(p.granule))
	binary.LittleEndian.PutUint32(buf[14:18], p.serial)
	binary.LittleEndian.PutUint32(buf[18:22], p.sequence)
	buf[26] = byte(len(p.segTable))
	copy(buf[27:27+len(p.segTable)], p.segTable)
	copy(buf[27+len(p.segTable):], p.payload)
	if want := independentOggCRC32(buf); want != p.storedCRC {
		t.Errorf("page seq=%d CRC = %#x, want %#x (independently recomputed)", p.sequence, p.storedCRC, want)
	}
}

// TestNormalizeOggOpusRepagesAndFixesHeader is DoD items 1+2 together: a
// Concentus-style file (248 packets/page, 48000 declared, no pre-skip)
// comes out with pages of <= 30 packets, a valid CRC on every page, and
// the header declaring 16000/312.
func TestNormalizeOggOpusRepagesAndFixesHeader(t *testing.T) {
	packets := fakeAudioPackets(500)
	input := buildOggOpusFile(t, 0, 48000, packets, 248)

	out := NormalizeOggOpus(input)

	pages := parseOggPagesForTest(t, out)
	if len(pages) < 2 {
		t.Fatalf("only %d pages, want at least OpusHead+OpusTags", len(pages))
	}
	if !bytes.HasPrefix(pages[0].payload, []byte("OpusHead")) {
		t.Fatalf("page 0 payload doesn't start with OpusHead: %v", pages[0].payload)
	}
	if got := binary.LittleEndian.Uint16(pages[0].payload[10:12]); got != oggTargetPreSkip {
		t.Errorf("pre-skip = %d, want %d", got, oggTargetPreSkip)
	}
	if got := binary.LittleEndian.Uint32(pages[0].payload[12:16]); got != oggTargetSampleRate {
		t.Errorf("sample rate = %d, want %d", got, oggTargetSampleRate)
	}
	if pages[0].headerType&0x02 == 0 {
		t.Error("page 0 missing the BOS flag")
	}
	if pages[len(pages)-1].headerType&0x04 == 0 {
		t.Error("last page missing the EOS flag")
	}
	for _, p := range pages {
		verifyPageCRC(t, p)
	}
	// The contract's own number (~30), hardcoded independently of
	// oggAudioPacketsPerPage — asserting against that constant instead
	// would make this check pass trivially for ANY value it happens to
	// hold, catching nothing if it ever drifted from the actual target.
	const wantMaxPacketsPerPage = 30
	for i, p := range pages[2:] { // audio pages only — OpusHead/OpusTags are single-packet by design
		if n := p.audioPacketCount(); n > wantMaxPacketsPerPage {
			t.Errorf("audio page %d has %d packets, want <= %d", i, n, wantMaxPacketsPerPage)
		}
	}
}

// TestNormalizeOggOpusKeepsPacketBytesIdentical is DoD item 3: the
// container is reorganized, the audio itself never is.
func TestNormalizeOggOpusKeepsPacketBytesIdentical(t *testing.T) {
	packets := fakeAudioPackets(80)
	input := buildOggOpusFile(t, 0, 48000, packets, 248)

	out := NormalizeOggOpus(input)

	gotPackets, _, ok := oggParsedPackets(out)
	if !ok {
		t.Fatal("normalized output didn't parse as valid OGG/Opus")
	}
	if len(gotPackets) != len(packets)+2 {
		t.Fatalf("packet count = %d, want %d (+2 header packets)", len(gotPackets), len(packets)+2)
	}
	for i, want := range packets {
		if !bytes.Equal(gotPackets[i+2], want) {
			t.Errorf("audio packet %d = %v, want %v (untouched)", i, gotPackets[i+2], want)
		}
	}
}

// TestNormalizeOggOpusPreservesTotalDuration is DoD item 4: the LAST
// page's granule position (the stream's total sample count) is the same
// value before and after repaging — nothing was lost or invented.
func TestNormalizeOggOpusPreservesTotalDuration(t *testing.T) {
	packets := fakeAudioPackets(133) // not an exact multiple of any page size involved
	input := buildOggOpusFile(t, 0, 48000, packets, 248)
	wantGranule := int64(len(packets)) * opusFrameSamples

	inPages := parseOggPagesForTest(t, input)
	if got := inPages[len(inPages)-1].granule; got != wantGranule {
		t.Fatalf("test fixture's own final granule = %d, want %d — a fixture bug, not NormalizeOggOpus", got, wantGranule)
	}

	out := NormalizeOggOpus(input)
	outPages := parseOggPagesForTest(t, out)
	if got := outPages[len(outPages)-1].granule; got != wantGranule {
		t.Errorf("final granule after repaging = %d, want %d (duration must not change)", got, wantGranule)
	}
}

// TestNormalizeOggOpusIsIdempotent is DoD item 5's criterion, spelled out
// in NormalizeOggOpus's own doc comment: the output is rebuilt entirely
// from the real packet stream, so normalizing an already-normalized file
// reproduces the identical bytes — a resent, already-compliant note
// (the contract's own example) never comes out worse a second time
// through.
func TestNormalizeOggOpusIsIdempotent(t *testing.T) {
	packets := fakeAudioPackets(200)
	input := buildOggOpusFile(t, 0, 48000, packets, 248)

	once := NormalizeOggOpus(input)
	twice := NormalizeOggOpus(once)

	if !bytes.Equal(once, twice) {
		t.Error("NormalizeOggOpus(NormalizeOggOpus(x)) != NormalizeOggOpus(x) — a re-sent already-normalized note would degrade")
	}
}

// TestNormalizeOggOpusPassesThroughUnparseableInput is DoD item 6: a
// file that isn't valid, complete OGG/Opus must go out UNCHANGED, never
// fail the send.
func TestNormalizeOggOpusPassesThroughUnparseableInput(t *testing.T) {
	garbage := []byte("this is not an ogg file at all")
	truncated := buildOggOpusFile(t, 0, 48000, fakeAudioPackets(5), 248)
	truncated = truncated[:len(truncated)-5] // chop mid-page — still starts OggS + OpusHead

	for name, data := range map[string][]byte{
		"not OGG at all":     garbage,
		"truncated mid-page": truncated,
	} {
		t.Run(name, func(t *testing.T) {
			got := NormalizeOggOpus(data)
			if !bytes.Equal(got, data) {
				t.Errorf("NormalizeOggOpus(%s) modified the input, want it returned unchanged", name)
			}
		})
	}
}
