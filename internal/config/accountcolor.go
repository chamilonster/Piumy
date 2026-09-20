package config

// Package-level color derivation for multi-account (S2/S3). Lives here, not
// in main (where S2 first put it, trayicon_recolor.go) — internal/restapi
// can't import main, so if the dashboard derived its OWN accent color,
// tray and tablero would eventually show different colors for the same
// account. internal/config is neutral ground both can import: the ONE
// place that decides WHAT color an account gets. main still decides HOW to
// paint an ICO with it (trayicon_recolor.go); internal/restapi just reports
// it (GET /api/status). S3, ct-2026-09-20-1202 — moved, not rewritten: same
// palette, same hash, same math as S2, just relocated.

import (
	"fmt"
	"hash/fnv"
	"math"
)

// brandR/G/B is the tray icon's own dominant fill color — the most frequent
// opaque pixel sampled directly from assets/tray.ico's 32×32 PNG payload
// (measured, not guessed), so rotating THIS by an account's delta lands on
// the same hex the real icon's pixels rotate to.
const brandR, brandG, brandB = 86, 245, 159

// accountHueDeltas is the fixed palette S2 picked — degrees to ADD to the
// brand hue, deterministic per account (accountHueIndex below). 7 values
// 45° apart, none 0 (0 would be the brand color unrotated): any two land at
// least 45° apart from each other AND from the original — never the
// few-degrees-apart near-miss a raw hash%360 could produce.
var accountHueDeltas = [...]float64{45, 90, 135, 180, 225, 270, 315}

// accountHueIndex picks account's palette slot via FNV-32a (stdlib,
// unseeded — deterministic across runs/processes, unlike Go's map hash):
// same account, same slot, always.
func accountHueIndex(account string) int {
	h := fnv.New32a()
	h.Write([]byte(account))
	return int(h.Sum32() % uint32(len(accountHueDeltas)))
}

// AccountColor is an account's single, shared visual identity. HueDelta
// drives the tray icon's own pixel-by-pixel rotation (main,
// trayicon_recolor.go — RecolorTrayIcon receives it, no longer computes
// it); Hex is that exact same rotation applied once to the brand's
// reference color, ready for anything that just needs to PAINT with it
// (the dashboard's CSS accent).
type AccountColor struct {
	HueDelta float64
	Hex      string // "" when Account == "" — S1's "no account" case
}

// ColorForAccount returns account's deterministic accent — the zero value
// (HueDelta 0, Hex "") for account == "". 0 is never in accountHueDeltas,
// so main's RecolorTrayIcon can safely treat HueDelta == 0 as "no
// account, leave the icon untouched" with no separate empty-string check.
func ColorForAccount(account string) AccountColor {
	if account == "" {
		return AccountColor{}
	}
	delta := accountHueDeltas[accountHueIndex(account)]
	h, s, v := RGBToHSV(brandR, brandG, brandB)
	h = math.Mod(h+delta, 360)
	if h < 0 {
		h += 360
	}
	r, g, b := HSVToRGB(h, s, v)
	return AccountColor{HueDelta: delta, Hex: fmt.Sprintf("#%02x%02x%02x", r, g, b)}
}

// RGBToHSV/HSVToRGB: standard HSV conversion (h in [0,360), s/v in [0,1]) —
// stdlib has no HSV type. Exported so main's trayicon_recolor.go (the
// pixel-by-pixel rotation — "cómo se pinta") reuses this SAME math instead
// of a second implementation that could quietly drift from the color this
// package decides on.
func RGBToHSV(r, g, b uint8) (h, s, v float64) {
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

func HSVToRGB(h, s, v float64) (r, g, b uint8) {
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
