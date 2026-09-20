// T97 (ct-2026-08-29): antes de este archivo, agentTracker no tenía test
// dedicado — sweepOnce (extraído de sweep) es directamente testeable sin un
// server MCP completo ni esperar el ticker de 10s real.
package mcpserver

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/state"
)

func newTestTracker(t *testing.T, idle time.Duration, pingAgent func(string) bool) *agentTracker {
	t.Helper()
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	return newAgentTracker(sm, idle, pingAgent)
}

// TestAgentTrackerSurvivesIdleWithLivePing is T97's core positive case
// (criterio de listo: "agente sin llamadas recientes pero con antena que
// responde sigue conectado").
func TestAgentTrackerSurvivesIdleWithLivePing(t *testing.T) {
	pinged := []string{}
	tr := newTestTracker(t, time.Minute, func(terminalID string) bool {
		pinged = append(pinged, terminalID)
		return true
	})
	tr.sessions["sess1"] = sessionInfo{lastSeen: time.Now().Add(-2 * time.Minute), terminalID: "term-alive"}
	// seen() would have set this true when the session was first created —
	// bypassed here since the test writes tr.sessions directly.
	_ = tr.state.Update(func(s *state.Status) { s.AgentConnected = true })

	tr.sweepOnce()

	if len(pinged) != 1 || pinged[0] != "term-alive" {
		t.Fatalf("pinged = %v, want exactly one ping to term-alive", pinged)
	}
	info, ok := tr.sessions["sess1"]
	if !ok {
		t.Fatal("session evicted despite a live ping — want it kept")
	}
	if time.Since(info.lastSeen) > 5*time.Second {
		t.Errorf("lastSeen = %v, want refreshed to ~now after a live ping", info.lastSeen)
	}
	if !tr.state.Snapshot().AgentConnected {
		t.Error("AgentConnected = false after a live ping, want true")
	}
}

// TestAgentTrackerEvictsWithDeadPingAndLogsReason is the negative half
// (criterio de listo: "antena que NO responde se marca desconectado con el
// motivo en el log" — not the old generic "agent idle").
func TestAgentTrackerEvictsWithDeadPingAndLogsReason(t *testing.T) {
	tr := newTestTracker(t, time.Minute, func(terminalID string) bool { return false })
	tr.sessions["sess1"] = sessionInfo{lastSeen: time.Now().Add(-2 * time.Minute), terminalID: "term-gone"}

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	tr.sweepOnce()

	if _, ok := tr.sessions["sess1"]; ok {
		t.Error("session survived a dead ping — want it evicted")
	}
	if tr.state.Snapshot().AgentConnected {
		t.Error("AgentConnected = true after a dead ping, want false")
	}
	logged := buf.String()
	if !strings.Contains(logged, "term-gone") || !strings.Contains(logged, "no respondió al ping") {
		t.Errorf("log = %q, want the real reason (terminal id + ping failure), not a generic idle line", logged)
	}
}

// TestAgentTrackerEvictionLogHonestWhenOtherSessionsRemain is the second
// remate from Citrino's audit: the old code logged "clearing
// AgentConnected" per EVICTED session, even when other sessions were still
// live and AgentConnected never actually changed. A log about a gateway
// that lies must not itself lie about whether it cleared anything.
func TestAgentTrackerEvictionLogHonestWhenOtherSessionsRemain(t *testing.T) {
	tr := newTestTracker(t, time.Minute, func(terminalID string) bool {
		return terminalID == "term-alive" // only the OTHER session answers
	})
	tr.sessions["sess-gone"] = sessionInfo{lastSeen: time.Now().Add(-2 * time.Minute), terminalID: "term-gone"}
	tr.sessions["sess-alive"] = sessionInfo{lastSeen: time.Now().Add(-2 * time.Minute), terminalID: "term-alive"}
	_ = tr.state.Update(func(s *state.Status) { s.AgentConnected = true })

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	tr.sweepOnce()

	if _, ok := tr.sessions["sess-gone"]; ok {
		t.Error("sess-gone survived a dead ping — want it evicted")
	}
	if _, ok := tr.sessions["sess-alive"]; !ok {
		t.Error("sess-alive got evicted — want it kept, its ping succeeded")
	}
	if !tr.state.Snapshot().AgentConnected {
		t.Error("AgentConnected = false with sess-alive still connected, want true")
	}
	logged := buf.String()
	if strings.Contains(logged, "clearing AgentConnected") {
		t.Errorf("log = %q, want it to NOT claim AgentConnected cleared — a live session remains", logged)
	}
}

// TestAgentTrackerNeverPingsActiveSessions is the third criterio de listo
// case: a session well within idleAfter must never trigger a ping — the
// explicit cost guard ("solo pinguear al que está por marcarse idle, no a
// todos").
func TestAgentTrackerNeverPingsActiveSessions(t *testing.T) {
	pingCalled := false
	tr := newTestTracker(t, time.Minute, func(string) bool {
		pingCalled = true
		return true
	})
	tr.sessions["sess1"] = sessionInfo{lastSeen: time.Now(), terminalID: "term-busy"}

	tr.sweepOnce()

	if pingCalled {
		t.Error("pingAgent called for a session still well within idleAfter — want it left alone")
	}
	if _, ok := tr.sessions["sess1"]; !ok {
		t.Error("active session evicted — want it kept without any ping")
	}
}

// TestAgentTrackerNoTerminalIDFallsBackToPlainEvict covers the shared-bucket
// edge case (sessionKey's own doc: "" for a caller with no identifiable
// session) — nothing to ping, so it can't claim "verified alive"; falls
// back to the pre-T97 unconditional evict, honestly logged as unverifiable
// rather than pretending a ping happened.
func TestAgentTrackerNoTerminalIDFallsBackToPlainEvict(t *testing.T) {
	pingCalled := false
	tr := newTestTracker(t, time.Minute, func(string) bool {
		pingCalled = true
		return true
	})
	tr.sessions["sess1"] = sessionInfo{lastSeen: time.Now().Add(-2 * time.Minute), terminalID: ""}

	tr.sweepOnce()

	if pingCalled {
		t.Error("pingAgent called with no terminalID to ping — want it skipped entirely")
	}
	if _, ok := tr.sessions["sess1"]; ok {
		t.Error("session with no terminalID survived — want it evicted, unverifiable")
	}
}

// TestAgentTrackerPingRaceDoesNotClobberRealActivity: a real tool call
// landing WHILE the probe is in flight (bounded, but network I/O — the real
// wiring is a handshake over HTTP, not instant) must win — neither a stale
// refresh nor a stale evict may overwrite what seen() just recorded.
// Simulated by having pingAgent itself call seen() for the same session
// before returning "dead", mimicking a genuine MCP call racing the probe.
func TestAgentTrackerPingRaceDoesNotClobberRealActivity(t *testing.T) {
	tr := newTestTracker(t, time.Minute, nil)
	tr.pingAgent = func(terminalID string) bool {
		tr.sessions["sess1"] = sessionInfo{lastSeen: time.Now(), terminalID: terminalID} // a real call landed mid-ping
		return false                                                                     // ...and the ping itself still times out
	}
	tr.sessions["sess1"] = sessionInfo{lastSeen: time.Now().Add(-2 * time.Minute), terminalID: "term-racy"}

	tr.sweepOnce()

	info, ok := tr.sessions["sess1"]
	if !ok {
		t.Fatal("real activity mid-ping got clobbered by a stale evict")
	}
	if time.Since(info.lastSeen) > 5*time.Second {
		t.Errorf("lastSeen = %v, want the real call's fresh timestamp preserved", info.lastSeen)
	}
}
