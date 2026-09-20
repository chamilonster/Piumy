package config

import "testing"

// TestColorForAccountEmptyIsZeroValue is S1/S2's own invariant preserved
// through the S3 move: no account, no color — main's RecolorTrayIcon relies
// on HueDelta == 0 meaning "leave the icon untouched".
func TestColorForAccountEmptyIsZeroValue(t *testing.T) {
	got := ColorForAccount("")
	if got.HueDelta != 0 || got.Hex != "" {
		t.Errorf("ColorForAccount(\"\") = %+v, want the zero value", got)
	}
}

// TestColorForAccountDeterministic: the same account, called twice, must
// return the identical color every time.
func TestColorForAccountDeterministic(t *testing.T) {
	a := ColorForAccount("trabajo")
	b := ColorForAccount("trabajo")
	if a != b {
		t.Errorf("ColorForAccount(\"trabajo\") twice = %+v then %+v, want identical", a, b)
	}
}

// TestColorForAccountSurvivedTheMove is S3's own explicit regression guard
// (ct-2026-09-20-1202, boss/Citrino: "es mudanza, no reescritura... si el
// color de una cuenta cambia después de mover, es bug de la mudanza") —
// exact values computed independently before this file existed, from the
// SAME palette/hash/brand-pixel this package now uses. A different result
// here means the move altered behavior, not just location.
func TestColorForAccountSurvivedTheMove(t *testing.T) {
	cases := []struct {
		account      string
		wantHueDelta float64
		wantHex      string
	}{
		{"trabajo", 270, "#f5ef56"},
		{"personal", 180, "#f556ac"},
	}
	for _, c := range cases {
		got := ColorForAccount(c.account)
		if got.HueDelta != c.wantHueDelta {
			t.Errorf("ColorForAccount(%q).HueDelta = %v, want %v", c.account, got.HueDelta, c.wantHueDelta)
		}
		if got.Hex != c.wantHex {
			t.Errorf("ColorForAccount(%q).Hex = %q, want %q", c.account, got.Hex, c.wantHex)
		}
	}
}

// TestColorForAccountDifferentAccountsCanDiffer: two accounts landing in
// different palette slots must get different colors — the whole point of
// S2/S3 (telling two live instances apart).
func TestColorForAccountDifferentAccountsCanDiffer(t *testing.T) {
	a := ColorForAccount("trabajo")
	b := ColorForAccount("personal")
	if a.HueDelta == b.HueDelta || a.Hex == b.Hex {
		t.Errorf("ColorForAccount(trabajo) = %+v, ColorForAccount(personal) = %+v, want different (test fixture picked two accounts in different palette slots)", a, b)
	}
}

// TestRGBToHSVHSVToRGBRoundTrip: converting a color to HSV and back must
// reproduce it within rounding — the invariant rotateHue (trayicon_recolor.go)
// depends on for every non-rotated channel (S/V/alpha must survive intact).
func TestRGBToHSVHSVToRGBRoundTrip(t *testing.T) {
	cases := [][3]uint8{{86, 245, 159}, {0, 0, 0}, {255, 255, 255}, {128, 128, 128}, {200, 50, 10}}
	for _, rgb := range cases {
		h, s, v := RGBToHSV(rgb[0], rgb[1], rgb[2])
		r, g, b := HSVToRGB(h, s, v)
		within := func(a, b uint8) bool {
			d := int(a) - int(b)
			if d < 0 {
				d = -d
			}
			return d <= 1
		}
		if !within(r, rgb[0]) || !within(g, rgb[1]) || !within(b, rgb[2]) {
			t.Errorf("RGBToHSV/HSVToRGB round trip of %v = (%d,%d,%d), want ~%v", rgb, r, g, b, rgb)
		}
	}
}
