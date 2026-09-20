package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"piumy-gateway/internal/config"
)

// makeTestICO builds a minimal 1-entry ICO container around img — same
// shape buildICO produces, used here to drive RecolorTrayIcon without
// depending on the real assets/tray.ico for pixel-level assertions.
func makeTestICO(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	entry := icoEntry{width: byte(b.Dx()), height: byte(b.Dy()), planes: 1, bitCount: 32}
	return buildICO([]icoEntry{entry}, [][]byte{buf.Bytes()})
}

func decodeICOFirstImage(t *testing.T, icoData []byte) image.Image {
	t.Helper()
	entries, err := parseICO(icoData)
	if err != nil {
		t.Fatalf("parseICO: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("parseICO: 0 entries")
	}
	e := entries[0]
	img, err := png.Decode(bytes.NewReader(icoData[e.imageOffset : e.imageOffset+e.bytesInRes]))
	if err != nil {
		t.Fatalf("decode entry 0: %v", err)
	}
	return img
}

// TestRecolorTrayIconZeroDeltaReturnsUnchanged is verification point 1 of
// S2's contract, still true after S3 moved color selection out of this
// package: no account -> config.ColorForAccount("").HueDelta == 0 -> the
// default install's 99% case never even enters the parsing path.
func TestRecolorTrayIconZeroDeltaReturnsUnchanged(t *testing.T) {
	src := []byte{1, 2, 3, 4, 5} // deliberately not a valid ICO — must never be parsed
	got, err := RecolorTrayIcon(src, 0)
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Errorf("RecolorTrayIcon(_, 0) = %v, want the input unchanged", got)
	}
}

// TestRecolorTrayIconSameDeltaDeterministic is verification point 3: the
// same color, applied twice, must produce identical bytes every time.
func TestRecolorTrayIconSameDeltaDeterministic(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 0, G: 200, B: 0, A: 255})
	ico := makeTestICO(t, img)

	a, err := RecolorTrayIcon(ico, 270)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RecolorTrayIcon(ico, 270)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("same delta applied twice produced different bytes, want identical")
	}
}

// TestRecolorTrayIconDifferentDeltasDifferentOutput: two different deltas
// must produce visibly different output — the whole point of S2/S3 (two
// live instances must be tellable apart).
func TestRecolorTrayIconDifferentDeltasDifferentOutput(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 0, G: 200, B: 0, A: 255})
	ico := makeTestICO(t, img)

	a, err := RecolorTrayIcon(ico, 270)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RecolorTrayIcon(ico, 180)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("two different deltas produced identical bytes, want different colors")
	}
}

// TestRecolorTrayIconPreservesSaturationValueAlpha covers the contract's
// core color rule: hue rotation only — a desaturated (gray) pixel and a
// fully transparent pixel must come out visually unchanged, and a
// saturated pixel's hue must actually have moved.
func TestRecolorTrayIconPreservesSaturationValueAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 0, G: 0, B: 0, A: 255})       // black, saturation 0
	img.Set(1, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 0})      // fully transparent
	img.Set(0, 1, color.NRGBA{R: 0, G: 200, B: 0, A: 255})     // saturated green
	img.Set(1, 1, color.NRGBA{R: 128, G: 128, B: 128, A: 255}) // gray, saturation 0
	ico := makeTestICO(t, img)

	got, err := RecolorTrayIcon(ico, 270)
	if err != nil {
		t.Fatal(err)
	}
	out := decodeICOFirstImage(t, got)

	within := func(a, b uint8, tol int) bool {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d <= tol
	}

	black := color.NRGBAModel.Convert(out.At(0, 0)).(color.NRGBA)
	if !within(black.R, 0, 2) || !within(black.G, 0, 2) || !within(black.B, 0, 2) {
		t.Errorf("black pixel moved: %+v, want ~black (saturation 0 must be hue-rotation-proof)", black)
	}

	transparent := color.NRGBAModel.Convert(out.At(1, 0)).(color.NRGBA)
	if transparent.A != 0 {
		t.Errorf("transparent pixel alpha = %d, want 0 (alpha must never change)", transparent.A)
	}

	gray := color.NRGBAModel.Convert(out.At(1, 1)).(color.NRGBA)
	if !within(gray.R, 128, 2) || !within(gray.G, 128, 2) || !within(gray.B, 128, 2) {
		t.Errorf("gray pixel moved: %+v, want ~128/128/128 (saturation 0 must be hue-rotation-proof)", gray)
	}

	green := color.NRGBAModel.Convert(out.At(0, 1)).(color.NRGBA)
	if green.G >= 190 && green.R < 20 && green.B < 20 {
		t.Errorf("saturated green pixel unchanged: %+v, want the hue actually rotated", green)
	}
}

// TestRecolorTrayIconMalformedContainerFallsBack is verification point 4:
// a broken ICO must fall back to the ORIGINAL bytes plus a non-nil error —
// never block startup, never ship a half-built icon.
func TestRecolorTrayIconMalformedContainerFallsBack(t *testing.T) {
	garbage := []byte("not an ico at all, just some bytes")
	got, err := RecolorTrayIcon(garbage, 270)
	if err == nil {
		t.Fatal("want an error for a malformed ICO container, got nil")
	}
	if !bytes.Equal(got, garbage) {
		t.Errorf("RecolorTrayIcon fallback = %v, want the original bytes unchanged", got)
	}
}

// TestRecolorTrayIconMalformedPNGPayloadFallsBack: the container parses
// fine but an entry's payload isn't a valid PNG — same fallback contract.
func TestRecolorTrayIconMalformedPNGPayloadFallsBack(t *testing.T) {
	entry := icoEntry{width: 1, height: 1, planes: 1, bitCount: 32}
	ico := buildICO([]icoEntry{entry}, [][]byte{[]byte("not a png")})

	got, err := RecolorTrayIcon(ico, 270)
	if err == nil {
		t.Fatal("want an error for a non-PNG payload, got nil")
	}
	if !bytes.Equal(got, ico) {
		t.Error("RecolorTrayIcon fallback changed the bytes, want the original ICO unchanged")
	}
}

// TestRecolorTrayIconRealAssetRoundTrips is a smoke check against the
// actual shipped icon (assets/tray.ico, 3 images per S2's own contract) —
// confidence beyond the synthetic fixtures above that the real container's
// shape parses and rebuilds cleanly.
func TestRecolorTrayIconRealAssetRoundTrips(t *testing.T) {
	data, err := os.ReadFile("assets/tray.ico")
	if err != nil {
		t.Skipf("assets/tray.ico not found relative to CWD: %v", err)
	}
	entriesBefore, err := parseICO(data)
	if err != nil {
		t.Fatalf("parseICO(real asset): %v", err)
	}

	got, err := RecolorTrayIcon(data, 270)
	if err != nil {
		t.Fatalf("RecolorTrayIcon(real asset): %v", err)
	}
	entriesAfter, err := parseICO(got)
	if err != nil {
		t.Fatalf("parseICO(recolored real asset): %v", err)
	}
	if len(entriesAfter) != len(entriesBefore) {
		t.Errorf("entry count = %d, want %d (unchanged)", len(entriesAfter), len(entriesBefore))
	}
	for i, e := range entriesAfter {
		if e.width != entriesBefore[i].width || e.height != entriesBefore[i].height {
			t.Errorf("entry %d size = %dx%d, want %dx%d", i, e.width, e.height, entriesBefore[i].width, entriesBefore[i].height)
		}
	}
}

// TestRecolorTrayIconMatchesConfigHex is S3's own central invariant
// (ct-2026-09-20-1202): the tray and the dashboard must NEVER show two
// different colors for the same account. Builds a 1×1 icon whose only
// pixel IS the exact brand reference color config.ColorForAccount rotates,
// recolors it with that same account's HueDelta, and asserts the result is
// EXACTLY config.ColorForAccount's own Hex — proof the two surfaces are
// structurally reading the same color, not just "probably the same".
func TestRecolorTrayIconMatchesConfigHex(t *testing.T) {
	want := config.ColorForAccount("trabajo")
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 86, G: 245, B: 159, A: 255}) // the exact brand reference pixel
	ico := makeTestICO(t, img)

	got, err := RecolorTrayIcon(ico, want.HueDelta)
	if err != nil {
		t.Fatal(err)
	}
	out := color.NRGBAModel.Convert(decodeICOFirstImage(t, got).At(0, 0)).(color.NRGBA)
	gotHex := fmt.Sprintf("#%02x%02x%02x", out.R, out.G, out.B)
	if gotHex != want.Hex {
		t.Errorf("RecolorTrayIcon of the brand pixel = %s, want config.ColorForAccount(\"trabajo\").Hex = %s", gotHex, want.Hex)
	}
}
