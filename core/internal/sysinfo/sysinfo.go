// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Camilo Brossard
//
// Package sysinfo: best-effort CPU/RAM readouts for the e-paper's tiny
// CPU/RAM bars (milestone E). Linux-only (/proc), same
// "stdlib + generic Linux interface, ok=false on any failure" contract as
// netinfo -- degrades to "no reading" everywhere else (a non-Linux dev
// host included), never fakes a number.
package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// CPUPercent returns the 1-minute load average scaled to 0..100 by core
// count (runtime.NumCPU()), or ok=false if /proc/loadavg is unavailable
// (non-Linux, or a read/parse failure).
func CPUPercent() (pct int, ok bool) {
	if runtime.GOOS != "linux" {
		return 0, false
	}
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	cores := runtime.NumCPU()
	if cores <= 0 {
		cores = 1
	}
	return clampPct(load1 / float64(cores) * 100), true
}

// RAMPercent returns used-RAM percent from /proc/meminfo
// ((MemTotal-MemAvailable)/MemTotal), or ok=false if unavailable.
func RAMPercent() (pct int, ok bool) {
	if runtime.GOOS != "linux" {
		return 0, false
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	var total, avail float64
	haveTotal, haveAvail := false, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, _ = strconv.ParseFloat(fields[1], 64)
			haveTotal = true
		case "MemAvailable:":
			avail, _ = strconv.ParseFloat(fields[1], 64)
			haveAvail = true
		}
		if haveTotal && haveAvail {
			break
		}
	}
	if !haveTotal || !haveAvail || total <= 0 {
		return 0, false
	}
	return clampPct((total - avail) / total * 100), true
}

func clampPct(p float64) int {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return int(p)
}
