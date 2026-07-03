// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package netinfo: best-effort network information (hostname, IP, WiFi level,
// SSID).
//
// All fields are advisory — failures return zero/nil values rather than errors.
// Safe for CGO_ENABLED=0 cross-compilation to linux/arm64: WiFi level/SSID use
// only stdlib + the `iw` CLI (already on Raspberry Pi OS, no new dependency).
//
// WiFi level is derived from /proc/net/wireless (Linux only). SSID comes from
// `iw dev <iface> link` (Linux only, generic — works on any distro with the
// `iw` package, not tied to a vendor lib per the portability rule). On all
// other platforms/failures both are nil/"" — the package compiles and runs
// cleanly on Windows, it just skips the Linux-only paths.
package netinfo

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Info holds a point-in-time snapshot of network information.
type Info struct {
	Hostname string // OS hostname + ".local", or override
	IP       string // first non-loopback IPv4 address; "" when offline
	Wifi     *int   // signal level 0..4 (0=not connected), nil if unknown
	SSID     string // current WiFi network name, "" if unknown/not connected
}

// Gather collects current network info best-effort. hostnameOverride, when
// non-empty, is used as-is instead of the OS hostname. wifiIface names the
// interface to query for SSID (e.g. "wlan0") — configurable, never hardcoded,
// since the interface name varies by board/dongle. Never returns an error;
// degraded or partial info is preferred over failure.
func Gather(hostnameOverride, wifiIface string) Info {
	return Info{
		Hostname: gatherHostname(hostnameOverride),
		IP:       gatherIP(),
		Wifi:     gatherWifi(),
		SSID:     gatherSSID(wifiIface),
	}
}

// gatherHostname returns hostnameOverride when set; otherwise os.Hostname()
// with ".local" appended (if not already present).
func gatherHostname(override string) string {
	if override != "" {
		return override
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return ""
	}
	if !strings.HasSuffix(h, ".local") {
		h += ".local"
	}
	return h
}

// gatherIP returns the first non-loopback IPv4 address found on any UP
// interface, or "" when none is available.
func gatherIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		// Skip loopback and interfaces that are down.
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}

// gatherWifi parses /proc/net/wireless (Linux only) and returns a signal level
// in the range 0..4, where 0 means "connected but no signal" and 4 is strongest.
// Returns nil on non-Linux systems or when the file is unavailable/unreadable.
func gatherWifi() *int {
	if runtime.GOOS != "linux" {
		return nil
	}
	return readProcNetWireless()
}

// readProcNetWireless parses /proc/net/wireless and returns the link quality
// of the first wireless interface scaled to 0..4. Returns nil on any failure.
//
// /proc/net/wireless format (after two header lines):
//
//	wlan0: 0000   45.  -65.  -256.   0   0   0   0   0   0
//
// Fields after the colon: status, link, level, noise, ...
// "link" is typically 0..70; we map that linearly to 0..4.
func readProcNetWireless() *int {
	data, err := os.ReadFile("/proc/net/wireless")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue // header lines have no ":"
		}
		// Everything after the colon: "0000   45.  -65.  ..."
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 2 {
			continue
		}
		// fields[0]=status, fields[1]=link quality (e.g. "45.")
		linkStr := strings.TrimSuffix(fields[1], ".")
		link, err := strconv.Atoi(linkStr)
		if err != nil {
			continue
		}
		// Scale 0..70 → 0..4 (integer division; clamp to [0,4]).
		level := link * 4 / 70
		if level > 4 {
			level = 4
		}
		if level < 0 {
			level = 0
		}
		return &level
	}
	return nil
}

// gatherSSID runs `iw dev <iface> link` (Linux only -- generic wireless
// tooling per the portability rule, no vendor-specific lib) and extracts the
// "SSID: <name>" line. Returns "" on non-Linux, a missing `iw` binary, no
// interface configured, or not currently associated -- never a placeholder.
func gatherSSID(iface string) string {
	if runtime.GOOS != "linux" || iface == "" {
		return ""
	}
	out, err := exec.Command("iw", "dev", iface, "link").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if ssid, ok := strings.CutPrefix(line, "SSID: "); ok {
			return ssid
		}
	}
	return ""
}
