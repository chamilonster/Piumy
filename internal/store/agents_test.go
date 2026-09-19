package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpsertAndGetAgent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := Agent{
		AgentID:           "term-secondary",
		Endpoint:          "http://192.168.1.10:8787",
		AntennaTerminalID: "ant-guid-1",
		Pinpass:           "abc123",
		Role:              "secondary",
	}
	if err := s.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.GetAgent("term-secondary")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("GetAgent: not found")
	}
	if got != a {
		t.Errorf("GetAgent = %+v, want %+v", got, a)
	}
}

func TestUpsertAgentOverwrites(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := Agent{AgentID: "term-x", Endpoint: "http://old:8787", AntennaTerminalID: "ant-old", Pinpass: "p1", Role: "secondary"}
	if err := s.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}
	a2 := Agent{AgentID: "term-x", Endpoint: "http://new:8787", AntennaTerminalID: "ant-new", Pinpass: "p2", Role: "secondary"}
	if err := s.UpsertAgent(a2); err != nil {
		t.Fatal(err)
	}

	got, _, _ := s.GetAgent("term-x")
	if got.Endpoint != "http://new:8787" {
		t.Errorf("after upsert Endpoint = %q, want http://new:8787", got.Endpoint)
	}
}

func TestGetAgentNotFound(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, ok, err := s.GetAgent("nonexistent")
	if err != nil || ok {
		t.Errorf("GetAgent(nonexistent) = (_, %v, %v), want (_, false, nil)", ok, err)
	}
}

func TestListAgents(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, a := range []Agent{
		{AgentID: "term-a", Endpoint: "http://a:8787", AntennaTerminalID: "ant-a", Pinpass: "pa", Role: "secondary"},
		{AgentID: "term-b", Endpoint: "http://b:8787", AntennaTerminalID: "ant-b", Pinpass: "pb", Role: "secondary"},
	} {
		if err := s.UpsertAgent(a); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListAgents = %d, want 2", len(list))
	}
	if list[0].AgentID != "term-a" || list[1].AgentID != "term-b" {
		t.Errorf("ListAgents order = [%s, %s], want [term-a, term-b]", list[0].AgentID, list[1].AgentID)
	}
}

// ── T129 (ct-2026-09-03-0200) — AgentsByAntenna ─────────────────────────────

func TestAgentsByAntennaFindsTheOwningAgent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.UpsertAgent(Agent{
		AgentID: "citrino2", Name: "Citrino", AntennaTerminalID: "capi-clevercoder-citrino-caaed305", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{
		AgentID: "otro-agente", AntennaTerminalID: "otra-antena", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.AgentsByAntenna("capi-clevercoder-citrino-caaed305")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AgentID != "citrino2" {
		t.Errorf("AgentsByAntenna = %+v, want exactly [citrino2]", got)
	}
}

func TestAgentsByAntennaReturnsAllSharersForAmbiguity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, id := range []string{"agent-uno", "agent-dos"} {
		if err := s.UpsertAgent(Agent{AgentID: id, AntennaTerminalID: "antenna-compartida", Role: "secondary"}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.AgentsByAntenna("antenna-compartida")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("AgentsByAntenna on a shared antenna = %d agents, want 2 — a caller needs the count to detect ambiguity", len(got))
	}
}

func TestAgentsByAntennaNoMatchReturnsEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.AgentsByAntenna("nadie-tiene-esta-antena")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("AgentsByAntenna with no match = %+v, want empty", got)
	}
}

func TestAgentsByAntennaEmptyInputReturnsEmptyWithoutQuerying(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.AgentsByAntenna("")
	if err != nil || len(got) != 0 {
		t.Errorf("AgentsByAntenna(\"\") = %+v, err=%v, want empty, nil error", got, err)
	}
}

// TestUpsertAgentWithName (M1, ct-2026-07-22-1301): name round-trips
// through UpsertAgent/GetAgent same as every other field.
func TestUpsertAgentWithName(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := Agent{AgentID: "term-named", Name: "Sonnet", Endpoint: "http://n:8787", AntennaTerminalID: "ant-n", Pinpass: "pn", Role: "secondary"}
	if err := s.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetAgent("term-named")
	if err != nil || !ok {
		t.Fatalf("GetAgent = (_, %v, %v)", ok, err)
	}
	if got.Name != "Sonnet" {
		t.Errorf("Name = %q, want Sonnet", got.Name)
	}

	// Overwrite with a new name — same "upsert replaces every field" contract
	// TestUpsertAgentOverwrites already checks for Endpoint.
	a.Name = "Sonnet renombrado"
	if err := s.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.GetAgent("term-named")
	if got.Name != "Sonnet renombrado" {
		t.Errorf("after upsert Name = %q, want %q", got.Name, "Sonnet renombrado")
	}
}

func TestDeleteAgent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := Agent{AgentID: "term-del", Endpoint: "http://del:8787", AntennaTerminalID: "ant-del", Pinpass: "pd", Role: "secondary"}
	if err := s.UpsertAgent(a); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAgent("term-del"); err != nil {
		t.Fatal(err)
	}
	_, ok, err := s.GetAgent("term-del")
	if err != nil || ok {
		t.Errorf("after Delete: GetAgent = (_, %v, %v), want (_, false, nil)", ok, err)
	}
}

// TestPrincipalAgentSynthesizesFromKV (ct-2026-07-29, agentes paso 3):
// PrincipalAgent reads the same 4 KV keys restapi.handleAgents used to read
// inline — round-trips through SetPrincipalAgent.
func TestPrincipalAgentSynthesizesFromKV(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, ok, err := s.PrincipalAgent(""); err != nil || ok {
		t.Errorf("PrincipalAgent(\"\") = (_, %v, %v), want (_, false, nil) — no principal configured", ok, err)
	}

	if err := s.SetPrincipalAgent("Boss", "http://127.0.0.1:8787", "ant-term", "s3cr3t=="); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.PrincipalAgent("principal-term")
	if err != nil || !ok {
		t.Fatalf("PrincipalAgent: ok=%v err=%v", ok, err)
	}
	want := Agent{
		AgentID: "principal-term", Name: "Boss", Endpoint: "http://127.0.0.1:8787",
		AntennaTerminalID: "ant-term", Pinpass: "s3cr3t==", Role: "principal",
	}
	if got != want {
		t.Errorf("PrincipalAgent = %+v, want %+v", got, want)
	}
}

// TestSetPrincipalAgentAllowsPrivateNetworkEndpoint is the regression test
// for ct-2026-07-29 (boss caught it same day: the first cut of this gate
// required literal 127.0.0.1, which breaks the product's actual target
// deploy — a Raspberry Pi running the gateway with the principal agent on a
// DIFFERENT machine of the same LAN, "y ahi no será local la antenita").
// A LAN IP (RFC1918), a bare "localhost", and an mDNS "*.local" name must
// all be accepted — none of them is a public address.
func TestSetPrincipalAgentAllowsPrivateNetworkEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://192.168.1.10:8787",  // Raspberry Pi on the LAN — the actual regression
		"http://10.0.0.5:8787",      // RFC1918 10/8
		"http://172.16.4.4:8787",    // RFC1918 172.16/12
		"http://localhost:8787",     // not a literal IP at all
		"http://raspberrypi.local:8787",
		"http://[::1]:8787",         // IPv6 loopback
		"http://[fd00::1]:8787",     // IPv6 ULA (RFC4193)
	} {
		s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetPrincipalAgent("Boss", endpoint, "ant-term", "s3cr3t=="); err != nil {
			t.Errorf("SetPrincipalAgent(%q): want no error, got %v", endpoint, err)
		}
		s.Close()
	}
}

// TestSetPrincipalAgentRejectsPublicEndpoint is the regression test for
// ct-2026-07-29 (agentes paso 3, closing a real gap: before this, nothing
// in the backend enforced "the principal's antenna is never public" — only
// the dashboard's readonly input did, cosmetically. CLAUDE.md: "el gate
// duro va en el código, no en skills ni prompts"). A public IP or a public
// domain must be rejected, not silently accepted — errors.Is against
// ErrPrincipalEndpointPublic so callers can map it to 400.
func TestSetPrincipalAgentRejectsPublicEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://8.8.8.8:8787",           // a public IP
		"http://antenita.example.com:8787", // a public domain
	} {
		s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
		if err != nil {
			t.Fatal(err)
		}
		err = s.SetPrincipalAgent("Boss", endpoint, "ant-term", "s3cr3t==")
		if !errors.Is(err, ErrPrincipalEndpointPublic) {
			t.Errorf("SetPrincipalAgent(%q): err = %v, want ErrPrincipalEndpointPublic", endpoint, err)
		}
		got, _, _ := s.PrincipalAgent("principal-term")
		if got.Endpoint != "" || got.Name != "" {
			t.Errorf("a rejected SetPrincipalAgent call must not have persisted anything, got %+v", got)
		}
		s.Close()
	}
}

// TestSetPrincipalAgentRejectsCloudMetadataEndpoint is T78's own regression
// (ct-2026-08-27-1952): 169.254.169.254 is the cloud-metadata endpoint every
// major provider (AWS/GCP/Azure) serves unauthenticated instance credentials
// from — the classic SSRF payoff, and reachable only because it happens to
// fall inside the link-local range the Raspberry Pi case already needs
// wide-open. Blocked as its own explicit case, ANY port, distinct from
// (and narrower than) rejecting link-local wholesale — see the sibling test
// below for the other half of that distinction.
func TestSetPrincipalAgentRejectsCloudMetadataEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://169.254.169.254:8787",       // the actual regression, cAPI's usual port
		"http://169.254.169.254:80/latest/meta-data/", // a different port + path — must not slip through on either
	} {
		s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
		if err != nil {
			t.Fatal(err)
		}
		err = s.SetPrincipalAgent("Boss", endpoint, "ant-term", "s3cr3t==")
		if !errors.Is(err, ErrPrincipalEndpointPublic) {
			t.Errorf("SetPrincipalAgent(%q): err = %v, want ErrPrincipalEndpointPublic", endpoint, err)
		}
		if err == nil || !strings.Contains(err.Error(), "cloud-metadata") {
			t.Errorf("SetPrincipalAgent(%q): err = %v, want it to name WHY (cloud-metadata) — not a bare rejection a reader would mistake for a config bug", endpoint, err)
		}
		got, _, _ := s.PrincipalAgent("principal-term")
		if got.Endpoint != "" {
			t.Errorf("a rejected SetPrincipalAgent call must not have persisted anything, got %+v", got)
		}
		s.Close()
	}
}

// TestSetPrincipalAgentAllowsOrdinaryLinkLocalEndpoint is the OTHER half of
// T78 (ct-2026-08-27-1952) — the one that protects the Raspberry Pi case
// from a future, well-intentioned over-hardening: blocking the ONE
// metadata address must never widen into blocking link-local in general.
// 169.254.0.0/16 minus that single /32 stays exactly as permissive as
// before this contract.
func TestSetPrincipalAgentAllowsOrdinaryLinkLocalEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://169.254.1.1:8787",
		"http://169.254.50.7:8787",
		"http://169.254.255.255:8787",
	} {
		s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetPrincipalAgent("Boss", endpoint, "ant-term", "s3cr3t=="); err != nil {
			t.Errorf("SetPrincipalAgent(%q): want no error (ordinary link-local, not the metadata address), got %v", endpoint, err)
		}
		s.Close()
	}
}

// TestSetPrincipalAgentAllowsEmptyEndpoint (T76, ct-2026-08-27-1752): an
// unconfigured principal (endpoint=="") is an already-supported state
// (S6), not an invalid one — PromoteToPrincipal needs this exact path to
// promote a secondary with no antenna wired yet.
func TestSetPrincipalAgentAllowsEmptyEndpoint(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetPrincipalAgent("Boss", "", "", ""); err != nil {
		t.Errorf("SetPrincipalAgent with empty endpoint: want no error, got %v", err)
	}
	got, ok, err := s.PrincipalAgent("principal-term")
	if err != nil || !ok || got.Name != "Boss" || got.Endpoint != "" {
		t.Errorf("PrincipalAgent = %+v, ok=%v, err=%v, want Name=Boss Endpoint=\"\"", got, ok, err)
	}
}

// TestPromoteToPrincipalSwapsRoles (T76, ct-2026-08-27-1752) is the core of
// "cambiar de rol": a secondary's own credentials become the principal's,
// and the OLD principal's data (endpoint/pinpass/name) survives under the
// promoted agent's OWN vacated agent_id — "no se borra, es un intercambio".
func TestPromoteToPrincipalSwapsRoles(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetPrincipalAgent("Vieja Principal", "http://192.168.1.10:8787", "ant-old", "pin-old"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{
		AgentID: "term-secundario", Name: "Nueva Principal", Endpoint: "http://192.168.1.20:8787",
		AntennaTerminalID: "ant-new", Pinpass: "pin-new", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	demotedID, err := s.PromoteToPrincipal("principal-term", "term-secundario")
	if err != nil {
		t.Fatalf("PromoteToPrincipal: %v", err)
	}
	if demotedID != "term-secundario" {
		t.Errorf("demotedID = %q, want term-secundario (the vacated slot, reused)", demotedID)
	}

	newPrincipal, ok, err := s.PrincipalAgent("principal-term")
	if err != nil || !ok {
		t.Fatalf("PrincipalAgent after promote: ok=%v err=%v", ok, err)
	}
	if newPrincipal.Name != "Nueva Principal" || newPrincipal.Endpoint != "http://192.168.1.20:8787" ||
		newPrincipal.AntennaTerminalID != "ant-new" || newPrincipal.Pinpass != "pin-new" {
		t.Errorf("new principal = %+v, want the promoted secondary's own credentials", newPrincipal)
	}

	demoted, ok, err := s.GetAgent("term-secundario")
	if err != nil || !ok {
		t.Fatalf("GetAgent(term-secundario) after promote: ok=%v err=%v", ok, err)
	}
	if demoted.Name != "Vieja Principal" || demoted.Endpoint != "http://192.168.1.10:8787" ||
		demoted.AntennaTerminalID != "ant-old" || demoted.Pinpass != "pin-old" || demoted.Role != "secondary" {
		t.Errorf("demoted agent = %+v, want the OLD principal's data intact, role=secondary", demoted)
	}
}

// TestPromoteToPrincipalBothDirections: swapping back (B->A after A->B)
// must work the same way — one function, no special-casing per direction.
func TestPromoteToPrincipalBothDirections(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetPrincipalAgent("A", "http://192.168.1.10:8787", "ant-a", "pin-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{AgentID: "term-b", Name: "B", Endpoint: "http://192.168.1.20:8787", AntennaTerminalID: "ant-b", Pinpass: "pin-b", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PromoteToPrincipal("principal-term", "term-b"); err != nil {
		t.Fatalf("A->B promote: %v", err)
	}
	// B is now principal; A lives at agent_id "term-b".
	if _, err := s.PromoteToPrincipal("principal-term", "term-b"); err != nil {
		t.Fatalf("B->A promote back: %v", err)
	}

	back, ok, err := s.PrincipalAgent("principal-term")
	if err != nil || !ok || back.Name != "A" || back.AntennaTerminalID != "ant-a" {
		t.Errorf("principal after swapping back = %+v, ok=%v, err=%v, want A's original data", back, ok, err)
	}
	secondary, ok, err := s.GetAgent("term-b")
	if err != nil || !ok || secondary.Name != "B" || secondary.AntennaTerminalID != "ant-b" {
		t.Errorf("term-b after swapping back = %+v, ok=%v, err=%v, want B's original data", secondary, ok, err)
	}
}

// TestPromoteToPrincipalAllowsUnconfiguredSecondary (T76, boss verbatim:
// "si el que sube no tiene antena configurada, subilo igual — el dueño la
// carga después. No lo bloquees por eso").
func TestPromoteToPrincipalAllowsUnconfiguredSecondary(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetPrincipalAgent("Vieja", "http://192.168.1.10:8787", "ant-old", "pin-old"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{AgentID: "term-nuevo", Name: "Sin Antena", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PromoteToPrincipal("principal-term", "term-nuevo"); err != nil {
		t.Fatalf("PromoteToPrincipal with no antenna configured: want no error, got %v", err)
	}
	got, ok, err := s.PrincipalAgent("principal-term")
	if err != nil || !ok || got.Name != "Sin Antena" || got.Endpoint != "" {
		t.Errorf("PrincipalAgent = %+v, ok=%v, err=%v, want Name=\"Sin Antena\" Endpoint=\"\"", got, ok, err)
	}
}

// TestPromoteToPrincipalRejectsUnknownAgent: promoting an agent_id with no
// `agents` row must fail cleanly, not silently wipe the principal.
func TestPromoteToPrincipalRejectsUnknownAgent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetPrincipalAgent("Vieja", "http://192.168.1.10:8787", "ant-old", "pin-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PromoteToPrincipal("principal-term", "nunca-registrado"); err == nil {
		t.Error("PromoteToPrincipal(unknown agent_id): want an error, got nil")
	}
	got, _, _ := s.PrincipalAgent("principal-term")
	if got.Name != "Vieja" {
		t.Errorf("principal after a rejected promote = %+v, want unchanged (Vieja)", got)
	}
}

// TestPromoteToPrincipalDeletesEmptyDemoted (T88, ct-2026-08-28-0626, boss:
// "hay 2 citrinos en opciones pero existe solo uno") — the case T76 warned
// about and didn't fix: if the principal going DOWN has nothing to
// preserve (no endpoint, no antenna — the state a fresh install or
// ClearPrincipalAgent leaves), the swap must not manufacture a ghost row at
// the promoted agent's vacated id. A Name alone (no endpoint/antenna) must
// NOT rescue it into the swap either — unreachable is unreachable, named or
// not.
func TestPromoteToPrincipalDeletesEmptyDemoted(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// El principal arranca vacío (instalación fresca / Selenita borrada) —
	// CON nombre, para probar que el nombre solo no lo salva del borrado.
	if err := s.SetPrincipalAgent("Selenita", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{
		AgentID: "term-citrino", Name: "Citrino", Endpoint: "http://192.168.1.20:8787",
		AntennaTerminalID: "ant-citrino", Pinpass: "pin-citrino", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}

	demotedID, err := s.PromoteToPrincipal("principal-term", "term-citrino")
	if err != nil {
		t.Fatalf("PromoteToPrincipal: %v", err)
	}
	if demotedID != "term-citrino" {
		t.Errorf("demotedID = %q, want term-citrino (the vacated slot's id, even though nothing was written there)", demotedID)
	}

	newPrincipal, ok, err := s.PrincipalAgent("principal-term")
	if err != nil || !ok || newPrincipal.Name != "Citrino" || newPrincipal.Endpoint != "http://192.168.1.20:8787" {
		t.Fatalf("principal after promote = %+v, ok=%v, err=%v, want Citrino's own credentials", newPrincipal, ok, err)
	}

	if _, ok, err := s.GetAgent("term-citrino"); err != nil || ok {
		t.Errorf("GetAgent(term-citrino) after promoting an empty principal = ok:%v err:%v, want no row at all (no ghost)", ok, err)
	}
}

// TestPromoteToPrincipalMigratesAssignments (T89, ct-2026-08-28-0628, boss:
// "hay 2 citrinos en opciones pero existe solo uno", el daño real que lo
// disparó) — a chat explicitly assigned to the PERSON being promoted must
// keep reaching that person, not the now-vacated agent_id. This is the
// heart of the contract: the same chat, untouched, must resolve to the
// NEW principal slot after the promotion.
func TestPromoteToPrincipalMigratesAssignments(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetPrincipalAgent("Vieja", "http://192.168.1.10:8787", "ant-old", "pin-old"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{
		AgentID: "term-citrino", Name: "Citrino", Endpoint: "http://192.168.1.20:8787",
		AntennaTerminalID: "ant-citrino", Pinpass: "pin-citrino", Role: "secondary",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchChat("555000001@s.whatsapp.net", "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus("555000001@s.whatsapp.net", AgentExclusiveStatus("term-citrino")); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PromoteToPrincipal("principal-term", "term-citrino"); err != nil {
		t.Fatalf("PromoteToPrincipal: %v", err)
	}

	chat, ok, err := s.GetChat("555000001@s.whatsapp.net")
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	want := AgentExclusiveStatus("principal-term")
	if chat.Status != want {
		t.Errorf("chat.Status after promote = %q, want %q (migrated to the principal's stable slot, following the person)", chat.Status, want)
	}
}

// TestPromoteToPrincipalLeavesOtherAssignmentsAlone (T89): the migration is
// scoped to the EXACT agent_id being vacated — a chat assigned to some
// OTHER agent, who isn't part of this promotion, must not move.
func TestPromoteToPrincipalLeavesOtherAssignmentsAlone(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetPrincipalAgent("Vieja", "http://192.168.1.10:8787", "ant-old", "pin-old"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{AgentID: "term-citrino", Name: "Citrino", Endpoint: "http://192.168.1.20:8787", AntennaTerminalID: "ant-citrino", Pinpass: "pin-citrino", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAgent(Agent{AgentID: "term-otro", Name: "Otro", Endpoint: "http://192.168.1.30:8787", AntennaTerminalID: "ant-otro", Pinpass: "pin-otro", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchChat("555000002@s.whatsapp.net", "Cliente de Otro", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus("555000002@s.whatsapp.net", AgentExclusiveStatus("term-otro")); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PromoteToPrincipal("principal-term", "term-citrino"); err != nil {
		t.Fatalf("PromoteToPrincipal: %v", err)
	}

	chat, ok, err := s.GetChat("555000002@s.whatsapp.net")
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	want := AgentExclusiveStatus("term-otro")
	if chat.Status != want {
		t.Errorf("chat.Status after an unrelated promote = %q, want unchanged %q — the migration swept too broadly", chat.Status, want)
	}
}

// TestClearPrincipalAgent (T76): "borrar el principal" — no agents row to
// delete, so this empties the KV slot back to the same state a fresh
// install starts in.
func TestClearPrincipalAgent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetPrincipalAgent("Boss", "http://192.168.1.10:8787", "ant-term", "s3cr3t=="); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearPrincipalAgent(); err != nil {
		t.Fatalf("ClearPrincipalAgent: %v", err)
	}
	got, ok, err := s.PrincipalAgent("principal-term")
	if err != nil || !ok || got.Name != "" || got.Endpoint != "" || got.AntennaTerminalID != "" || got.Pinpass != "" {
		t.Errorf("PrincipalAgent after clear = %+v, ok=%v, err=%v, want all empty (fresh-install state)", got, ok, err)
	}
}

// TestUnassignAllChatsForAgent is the regression test for ct-2026-07-29
// (boss: "ningún chat queda apuntando a un agente que ya no existe"): every
// chat assigned to agentID reverts to "new"; a chat assigned to a DIFFERENT
// agent, or never assigned at all, must be untouched.
func TestUnassignAllChatsForAgent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, jid := range []string{"1@c.us", "2@c.us", "3@c.us", "4@c.us"} {
		if err := s.TouchChat(jid, "", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetStatus("1@c.us", AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus("2@c.us", AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus("3@c.us", AgentExclusiveStatus("term-b")); err != nil {
		t.Fatal(err)
	}
	// 4@c.us stays unassigned ("new", TouchChat's default).

	n, err := s.UnassignAllChatsForAgent("term-a")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("UnassignAllChatsForAgent returned %d, want 2", n)
	}

	c1, _, _ := s.GetChat("1@c.us")
	c2, _, _ := s.GetChat("2@c.us")
	c3, _, _ := s.GetChat("3@c.us")
	c4, _, _ := s.GetChat("4@c.us")
	if c1.Status != "new" || c2.Status != "new" {
		t.Errorf("term-a's chats: 1=%q 2=%q, want both new", c1.Status, c2.Status)
	}
	if c3.Status != "agent_exclusive:term-b" {
		t.Errorf("term-b's chat = %q, want untouched", c3.Status)
	}
	if c4.Status != "new" {
		t.Errorf("never-assigned chat = %q, want new (untouched)", c4.Status)
	}
}

// TestAgentExclusiveIDAndStatus (M3/M4, ct-2026-07-22-1301): the one place
// the "agent_exclusive:<id>" chats.status form is built/parsed — shared by
// capipush's dispatch routing (M4) and the assign/unassign REST+MCP paths.
func TestAgentExclusiveIDAndStatus(t *testing.T) {
	if got := AgentExclusiveStatus("term-x"); got != "agent_exclusive:term-x" {
		t.Errorf("AgentExclusiveStatus = %q, want agent_exclusive:term-x", got)
	}
	cases := []struct {
		status string
		wantID string
		wantOK bool
	}{
		{"agent_exclusive:term-x", "term-x", true},
		{"agent_exclusive:", "", false}, // malformed: no id — same rule validChatStatus applies
		{"whitelist", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		id, ok := AgentExclusiveID(c.status)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("AgentExclusiveID(%q) = (%q, %v), want (%q, %v)", c.status, id, ok, c.wantID, c.wantOK)
		}
	}
}

// TestChatsForAgent (M3, ct-2026-07-22-1301): only chats explicitly
// assigned to agentID come back — everything else (unassigned, assigned to
// a DIFFERENT agent) is excluded.
func TestChatsForAgent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().Unix()
	for _, jid := range []string{"1@s.whatsapp.net", "2@s.whatsapp.net", "3@s.whatsapp.net"} {
		if err := s.TouchChat(jid, "", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetStatus("1@s.whatsapp.net", AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus("2@s.whatsapp.net", AgentExclusiveStatus("term-b")); err != nil {
		t.Fatal(err)
	}
	// 3@s.whatsapp.net stays unassigned (default status from TouchChat).

	got, err := s.ChatsForAgent("term-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].JID != "1@s.whatsapp.net" {
		t.Errorf("ChatsForAgent(term-a) = %+v, want exactly [1@s.whatsapp.net]", got)
	}
}
