package main

// Recolors the embedded tray icon in memory, per account (S2,
// ct-2026-09-20-1134) — assets/tray.ico itself is NEVER touched on disk,
// it's the brand's source of truth. Deliberately its own file with NO
// build tag: this is pure image/container arithmetic, nothing Windows-
// specific, so its test runs on every platform even though only
// tray_windows.go calls it today.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math"
)

// hueDeltas is the fixed palette S2's contract calls for — degrees to ADD
// to every pixel's hue, picked deterministically per account (hueIndex
// below). 7 values 45° apart, none 0 (0 would be the brand green
// unchanged): any two are at least 45° apart from each other AND from the
// original color, never the few-degrees-apart near-miss a raw
// hash%360 could produce.
var hueDeltas = [...]float64{45, 90, 135, 180, 225, 270, 315}

// hueIndex picks account's palette slot via FNV-32a (stdlib, unseeded —
// deterministic across runs and processes, unlike Go's map hash) — same
// account, same slot, always.
func hueIndex(account string) int {
	h := fnv.New32a()
	h.Write([]byte(account))
	return int(h.Sum32() % uint32(len(hueDeltas)))
}

// icoHeaderSize/icoEntrySize are the classic ICONDIR/ICONDIRENTRY sizes —
// see docs/S2-DIAGRAMA-DISTINTIVO-VISUAL.md for the byte layout, verified
// against the real assets/tray.ico before writing this.
const (
	icoHeaderSize = 6
	icoEntrySize  = 16
)

type icoEntry struct {
	width, height, colorCount, reserved byte
	planes, bitCount                    uint16
	bytesInRes, imageOffset             uint32
}

// RecolorTrayIcon returns icoData with every embedded image's hue rotated
// for account, or icoData UNCHANGED (same slice, byte-identical) when
// account is empty — the default install's 99% case never even enters the
// parsing path. Any failure (bad container, bad PNG, encode error) returns
// icoData unchanged plus a non-nil error — the caller logs it and keeps
// going with the original icon; a broken recolor must never block startup
// or ship a broken icon.
func RecolorTrayIcon(icoData []byte, account string) ([]byte, error) {
	if account == "" {
		return icoData, nil
	}
	entries, err := parseICO(icoData)
	if err != nil {
		return icoData, err
	}
	delta := hueDeltas[hueIndex(account)]

	payloads := make([][]byte, len(entries))
	for i, e := range entries {
		if uint64(e.imageOffset)+uint64(e.bytesInRes) > uint64(len(icoData)) {
			return icoData, fmt.Errorf("trayicon: entry %d: payload out of bounds", i)
		}
		raw := icoData[e.imageOffset : e.imageOffset+e.bytesInRes]
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return icoData, fmt.Errorf("trayicon: entry %d: decode png: %w", i, err)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, rotateHue(img, delta)); err != nil {
			return icoData, fmt.Errorf("trayicon: entry %d: encode png: %w", i, err)
		}
		payloads[i] = buf.Bytes()
	}
	return buildICO(entries, payloads), nil
}

// parseICO reads the ICONDIR header and its ICONDIRENTRY table — the
// pixel data itself is read later, per entry, straight from icoData.
func parseICO(icoData []byte) ([]icoEntry, error) {
	if len(icoData) < icoHeaderSize {
		return nil, fmt.Errorf("trayicon: %d bytes, shorter than the ICONDIR header", len(icoData))
	}
	if imgType := binary.LittleEndian.Uint16(icoData[2:4]); imgType != 1 {
		return nil, fmt.Errorf("trayicon: ICONDIR type %d, want 1 (icon)", imgType)
	}
	count := int(binary.LittleEndian.Uint16(icoData[4:6]))
	need := icoHeaderSize + count*icoEntrySize
	if len(icoData) < need {
		return nil, fmt.Errorf("trayicon: %d bytes, shorter than header+%d entries (%d)", len(icoData), count, need)
	}
	entries := make([]icoEntry, count)
	for i := range entries {
		off := icoHeaderSize + i*icoEntrySize
		entries[i] = icoEntry{
			width:       icoData[off],
			height:      icoData[off+1],
			colorCount:  icoData[off+2],
			reserved:    icoData[off+3],
			planes:      binary.LittleEndian.Uint16(icoData[off+4 : off+6]),
			bitCount:    binary.LittleEndian.Uint16(icoData[off+6 : off+8]),
			bytesInRes:  binary.LittleEndian.Uint32(icoData[off+8 : off+12]),
			imageOffset: binary.LittleEndian.Uint32(icoData[off+12 : off+16]),
		}
	}
	return entries, nil
}

// buildICO re-packs entries (everything but bytesInRes/imageOffset kept
// verbatim from the original) with payloads laid out right after the
// header+directory, in order — same shape parseICO reads, new offsets
// since the re-encoded PNGs are rarely the exact same size as the originals.
func buildICO(entries []icoEntry, payloads [][]byte) []byte {
	var out bytes.Buffer
	header := make([]byte, icoHeaderSize)
	binary.LittleEndian.PutUint16(header[2:4], 1) // type: icon
	binary.LittleEndian.PutUint16(header[4:6], uint16(len(entries)))
	out.Write(header)

	offset := uint32(icoHeaderSize + len(entries)*icoEntrySize)
	for i, e := range entries {
		entry := make([]byte, icoEntrySize)
		entry[0], entry[1], entry[2], entry[3] = e.width, e.height, e.colorCount, e.reserved
		binary.LittleEndian.PutUint16(entry[4:6], e.planes)
		binary.LittleEndian.PutUint16(entry[6:8], e.bitCount)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(payloads[i])))
		binary.LittleEndian.PutUint32(entry[12:16], offset)
		out.Write(entry)
		offset += uint32(len(payloads[i]))
	}
	for _, p := range payloads {
		out.Write(p)
	}
	return out.Bytes()
}

// rotateHue returns a copy of img with every pixel's hue shifted by
// deltaDegrees, saturation/value/alpha untouched. Converts through
// color.NRGBA (straight, non-premultiplied alpha) before the HSV math —
// premultiplied values would scale RGB toward black as alpha drops,
// distorting the hue of the icon's anti-aliased edge pixels.
func rotateHue(img image.Image, deltaDegrees float64) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			h, s, v := rgbToHSV(c.R, c.G, c.B)
			h = math.Mod(h+deltaDegrees, 360)
			if h < 0 {
				h += 360
			}
			r, g, bl := hsvToRGB(h, s, v)
			out.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: bl, A: c.A})
		}
	}
	return out
}

// rgbToHSV/hsvToRGB: standard HSV conversion (h in [0,360), s/v in [0,1]) —
// stdlib has no HSV type, image/color only ever deals in RGB-family models.
func rgbToHSV(r, g, b uint8) (h, s, v float64) {
	rf, gf, bf := float64(r)/255, float64(g)/255, float64(b)/255
	max := math.Max(rf, math.Max(gf, bf))
	min := math.Min(rf, math.Min(gf, bf))
	v = max
	d := max - min
	if max > 0 {
		s = d / max
	}
	if d == 0 {
		return 0, s, v
	}
	switch max {
	case rf:
		h = math.Mod((gf-bf)/d, 6)
	case gf:
		h = (bf-rf)/d + 2
	default:
		h = (rf-gf)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, v
}

func hsvToRGB(h, s, v float64) (r, g, b uint8) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var rf, gf, bf float64
	switch {
	case h < 60:
		rf, gf, bf = c, x, 0
	case h < 120:
		rf, gf, bf = x, c, 0
	case h < 180:
		rf, gf, bf = 0, c, x
	case h < 240:
		rf, gf, bf = 0, x, c
	case h < 300:
		rf, gf, bf = x, 0, c
	default:
		rf, gf, bf = c, 0, x
	}
	return clamp255(rf + m), clamp255(gf + m), clamp255(bf + m)
}

func clamp255(f float64) uint8 {
	v := math.Round(f * 255)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
