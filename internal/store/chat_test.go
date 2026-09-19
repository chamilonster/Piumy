package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTouchChatConfirmationModeByType(t *testing.T) {
	s := openTestStore(t)

	if err := s.TouchChat("111@c.us", "Ana", 100); err != nil {
		t.Fatalf("TouchChat 1-1: %v", err)
	}
	c, ok, err := s.GetChat("111@c.us")
	if err != nil || !ok {
		t.Fatalf("GetChat 1-1: ok=%v err=%v", ok, err)
	}
	if c.ConfirmationMode != "none" || c.Status != "new" {
		t.Errorf("1-1 chat: got confirmation_mode=%q status=%q, want none/new", c.ConfirmationMode, c.Status)
	}

	if err := s.TouchChat("222@g.us", "Grupo", 100); err != nil {
		t.Fatalf("TouchChat group: %v", err)
	}
	g, ok, err := s.GetChat("222@g.us")
	if err != nil || !ok {
		t.Fatalf("GetChat group: ok=%v err=%v", ok, err)
	}
	if g.ConfirmationMode != "always" || g.Status != "ignored" {
		t.Errorf("group chat: got confirmation_mode=%q status=%q, want always/ignored", g.ConfirmationMode, g.Status)
	}
}

// TestTouchChatStripsDeviceSuffix is T45's own reproduction
// (ct-2026-08-10-1424): a JID carrying WhatsApp's device suffix
// ("usuario:NN@s.whatsapp.net" — client.Store.ID's own form, never a
// conversation identity) must land in the chat WITHOUT it, and must not
// create a second, unreachable row alongside it — measured against the
// owner's live installation, exactly this: their own account duplicated,
// one row nothing could ever deliver to.
func TestTouchChatStripsDeviceSuffix(t *testing.T) {
	s := openTestStore(t)
	if err := s.TouchChat("55500000090:15@s.whatsapp.net", "Yo", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}

	if _, ok, err := s.GetChat("55500000090:15@s.whatsapp.net"); err != nil || ok {
		t.Errorf("GetChat(jid con sufijo) ok=%v err=%v, want ok=false — no debe quedar una fila bajo el jid crudo", ok, err)
	}
	c, ok, err := s.GetChat("55500000090@s.whatsapp.net")
	if err != nil || !ok {
		t.Fatalf("GetChat(jid limpio) ok=%v err=%v, want ok=true", ok, err)
	}
	if c.Name != "Yo" {
		t.Errorf("Name = %q, want %q", c.Name, "Yo")
	}

	chats, err := s.ListChats(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Errorf("ListChats = %d filas, want exactamente 1 — no debe crearse una segunda fila", len(chats))
	}
}

// TestTouchChatLeavesLIDAndGroupJIDsUntouched: @lid and @g.us never carry a
// device suffix and StripDeviceSuffix must leave them exactly as they came
// — this only touches the @s.whatsapp.net form.
func TestTouchChatLeavesLIDAndGroupJIDsUntouched(t *testing.T) {
	s := openTestStore(t)
	lid := "555000000000090@lid"
	group := "555000000090@g.us"
	if err := s.TouchChat(lid, "Alguien", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchChat(group, "Grupo", 100); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetChat(lid); err != nil || !ok {
		t.Errorf("GetChat(@lid) ok=%v err=%v, want ok=true bajo el jid original, sin tocar", ok, err)
	}
	if _, ok, err := s.GetChat(group); err != nil || !ok {
		t.Errorf("GetChat(@g.us) ok=%v err=%v, want ok=true bajo el jid original, sin tocar", ok, err)
	}
}

// TestTouchChatWithAndWithoutSuffixMergeIntoOneChat is the reverse case
// T45 flags as the one that matters: a real device suffix isn't a fixed
// property of a number — it can arrive with the suffix on one event and
// without it on the next (or vice versa). Both must land in the SAME row.
func TestTouchChatWithAndWithoutSuffixMergeIntoOneChat(t *testing.T) {
	s := openTestStore(t)
	if err := s.TouchChat("55500000091@s.whatsapp.net", "Primero", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchChat("55500000091:7@s.whatsapp.net", "Segundo", 200); err != nil {
		t.Fatal(err)
	}

	c, ok, err := s.GetChat("55500000091@s.whatsapp.net")
	if err != nil || !ok {
		t.Fatalf("GetChat ok=%v err=%v, want ok=true", ok, err)
	}
	if c.LastTS != 200 {
		t.Errorf("LastTS = %d, want 200 — el segundo TouchChat debe actualizar la MISMA fila, no crear otra", c.LastTS)
	}

	chats, err := s.ListChats(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 {
		t.Errorf("ListChats = %d filas, want exactamente 1 — con y sin sufijo caen en el mismo chat", len(chats))
	}
}

// TestStripDeviceSuffix is the unit-level coverage for the parsing itself —
// the branches TouchChat/AddMessage's own tests exercise indirectly.
func TestStripDeviceSuffix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"55500000090:15@s.whatsapp.net", "55500000090@s.whatsapp.net"},
		{"55500000090@s.whatsapp.net", "55500000090@s.whatsapp.net"},
		{"555000000000090@lid", "555000000000090@lid"},
		{"555000000090@g.us", "555000000090@g.us"},
		{"", ""},
	}
	for _, c := range cases {
		if got := StripDeviceSuffix(c.in); got != c.want {
			t.Errorf("StripDeviceSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMergeDeviceSuffixedOwnChatMovesMessagesAndDeletesDirtyRows is T118's
// own regression (ct-2026-09-01-2205): T117's resolveChatJID/resolveSenderJID
// fix stops a NEW device-suffixed own-jid row from being created, but an
// installation that already had one (Citrino's measurement against the
// real database: the owner's chat duplicated, one clean row and one
// suffixed) keeps carrying it forever without an active repair. This
// covers the fusion, not just the deletion: a message under the dirty jid
// must survive under the clean one, not vanish.
func TestMergeDeviceSuffixedOwnChatMovesMessagesAndDeletesDirtyRows(t *testing.T) {
	s := openTestStore(t)
	clean := "555000001@s.whatsapp.net"
	dirty := "555000001:15@s.whatsapp.net"

	if err := s.TouchChat(clean, "Yo", 100); err != nil {
		t.Fatal(err)
	}
	// The dirty row bypasses TouchChat on purpose — that's exactly how the
	// real bug wrote it (SyncRouterMode, pre-T117, never normalized).
	if _, err := s.db.Exec(`INSERT INTO chats (jid, name, last_ts) VALUES (?, ?, ?)`, dirty, "Yo", 90); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: dirty, ID: "msg-under-dirty", FromMe: true, Text: "nota", TS: 90}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAvatar(Avatar{JID: dirty, Path: "stale.jpg", NextCheckAt: 999999999}); err != nil {
		t.Fatal(err)
	}

	if err := s.MergeDeviceSuffixedOwnChat(clean); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.GetChat(dirty); err != nil || ok {
		t.Errorf("GetChat(dirty) ok=%v err=%v, want ok=false — the dirty row must be deleted", ok, err)
	}
	if _, ok, err := s.GetChat(clean); err != nil || !ok {
		t.Errorf("GetChat(clean) ok=%v err=%v, want ok=true — the clean row must survive", ok, err)
	}
	msgs, err := s.GetMessages(clean, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.ID == "msg-under-dirty" {
			found = true
		}
	}
	if !found {
		t.Errorf("message %q not found under the clean jid after merge — history was lost, not fused", "msg-under-dirty")
	}
	if _, ok, err := s.GetAvatar(dirty); err != nil || ok {
		t.Errorf("GetAvatar(dirty) ok=%v err=%v, want ok=false — the ghost avatar row must be deleted too", ok, err)
	}
}

// TestMergeDeviceSuffixedOwnChatNoOpWhenNothingDirty: the common case after
// the first boot that runs this — nothing to merge, nothing to touch.
func TestMergeDeviceSuffixedOwnChatNoOpWhenNothingDirty(t *testing.T) {
	s := openTestStore(t)
	clean := "555000002@s.whatsapp.net"
	if err := s.TouchChat(clean, "Yo", 100); err != nil {
		t.Fatal(err)
	}

	if err := s.MergeDeviceSuffixedOwnChat(clean); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.GetChat(clean); err != nil || !ok {
		t.Errorf("GetChat(clean) ok=%v err=%v, want ok=true unchanged", ok, err)
	}
}

// TestMigrateRequiredToAlwaysReconcilesLegacyValue covers the F4c audit
// fix directly: a row a pre-fix build wrote with the legacy 'required'
// value gets reconciled to 'always' on open (send_message's gate only
// checks "always" — "required" silently disabled the fail-safe).
func TestMigrateRequiredToAlwaysReconcilesLegacyValue(t *testing.T) {
	s := openTestStore(t)
	if err := s.TouchChat("333@g.us", "Legacy", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfirmationMode("333@g.us", "required"); err != nil {
		t.Fatal(err)
	}
	if err := migrateRequiredToAlways(s.db); err != nil {
		t.Fatal(err)
	}
	c, _, err := s.GetChat("333@g.us")
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfirmationMode != "always" {
		t.Errorf("confirmation_mode after migrateRequiredToAlways = %q, want always", c.ConfirmationMode)
	}
}

func TestSetModeNormalizesLegacyAdvanced(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetMode("111@c.us", "advanced"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	c, ok, err := s.GetChat("111@c.us")
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Mode != "dedicated" {
		t.Errorf("got mode=%q, want 'advanced' normalized to 'dedicated'", c.Mode)
	}
}

// TestSetActiveSweepsOldBacklogOnActivation is S8's own regression (ct-
// 2026-07-30-031126): activating a chat with months of unhandled backlog
// used to dump ALL of it into PendingDedicated the instant active flipped,
// ts ASC — the boss's own live smoke (a real contact's chat) jumped the
// queue from 76 to 82 instantly, with a message from February ahead of
// today's conversation. Activating means "de ahora en adelante", not
// "reprocesá su historia completa".
func TestSetActiveSweepsOldBacklogOnActivation(t *testing.T) {
	s := openTestStore(t)
	jid := "555000001@c.us"
	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	// Old backlog arrives while the chat is still inactive (the default).
	if err := s.AddMessage(Message{ChatJID: jid, ID: "old1", FromMe: false, Text: "Hola! Con quién hablo?", TS: 1772038322}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "old2", FromMe: false, Text: "otro mensaje viejo", TS: 1772038400}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("PendingDedicated right after activation = %+v, want 0 (old backlog swept to handled, not dumped into the live queue)", pending)
	}

	// Cuidado del contrato: no borrar, no ocultar del historial.
	msgs, err := s.GetMessages(jid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("GetMessages after activation = %d, want 2 — the backlog must stay fully visible, never deleted or hidden", len(msgs))
	}
}

// TestSetActiveSweepPreservesMessageThatPromptedActivation is Citrino's own
// catch on the first S8 pass: sweeping everything up to `now` swallowed the
// EXACT message that prompted the activation — the boss's real flow is
// "atendé a este número" (verbatim, 2026-07-30) AFTER spotting a message
// that just arrived, seconds to minutes earlier, never a simultaneous
// coincidence. A message from a minute ago must survive the sweep and
// still dispatch, while genuinely old backlog (same chat, same activation)
// still gets swept — both truths at once, or the fix just moves the bug.
func TestSetActiveSweepPreservesMessageThatPromptedActivation(t *testing.T) {
	s := openTestStore(t)
	// T114 (ct-2026-09-01-1905): era un identificador de 7 dígitos sin el
	// prefijo 555 — ambiguo (¿real o inventado?), y la ambigüedad es
	// exactamente lo que dejó pasar el hueco del hook. Sin forma de
	// confirmar si era real, se reemplaza por 555 — el test no depende del
	// valor específico, solo de que sea un jid válido usado consistente.
	jid := "5550009849@c.us"
	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Old backlog — months old, must still be swept.
	if err := s.AddMessage(Message{ChatJID: jid, ID: "old", FromMe: false, Text: "viejo, de meses atrás", TS: now.Add(-90 * 24 * time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	// The message that prompted "atendé a este número" — arrived a minute
	// before the boss's order, well inside activationSweepWindow's default.
	if err := s.AddMessage(Message{ChatJID: jid, ID: "trigger", FromMe: false, Text: "hola, necesito ayuda", TS: now.Add(-1 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "trigger" {
		t.Fatalf("PendingDedicated after activation = %+v, want exactly the 1-minute-old message that prompted it — swallowing it means the agent replies to someone without ever seeing what they said", pending)
	}
}

// TestSetActiveDoesNotResweepAlreadyActiveChat guards the transition check
// itself: a redundant SetActive(jid, true) call on an ALREADY active chat
// (e.g. set_config_level re-confirming a level that was already set) must
// never sweep a message the agent genuinely hasn't gotten to yet — only a
// real inactive→active transition triggers the sweep.
func TestSetActiveDoesNotResweepAlreadyActiveChat(t *testing.T) {
	s := openTestStore(t)
	jid := "already-active@c.us"
	if err := s.SetMode(jid, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: jid, ID: "fresh", FromMe: false, Text: "hola", TS: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}

	// Redundant re-activation — must be a no-op regarding messages.
	if err := s.SetActive(jid, true); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "fresh" {
		t.Errorf("PendingDedicated after a redundant re-activation = %+v, want the fresh message left untouched", pending)
	}
}

// TestEnrichChatLastText covers enrichChat's LastText fill — reuses the
// same LastMessage call already made for LastSpeaker, no extra query.
func TestEnrichChatLastText(t *testing.T) {
	s := openTestStore(t)
	if err := s.TouchChat("111@c.us", "Ana", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	c, ok, err := s.GetChat("111@c.us")
	if err != nil || !ok {
		t.Fatalf("GetChat before any message: ok=%v err=%v", ok, err)
	}
	if c.LastText != "" {
		t.Errorf("LastText with no messages = %q, want empty", c.LastText)
	}

	if err := s.AddMessage(Message{ChatJID: "111@c.us", ID: "m1", FromMe: false, Text: "hola", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "111@c.us", ID: "m2", FromMe: true, Text: "qué tal", TS: 200}); err != nil {
		t.Fatal(err)
	}
	c, ok, err = s.GetChat("111@c.us")
	if err != nil || !ok {
		t.Fatalf("GetChat after messages: ok=%v err=%v", ok, err)
	}
	if c.LastText != "qué tal" {
		t.Errorf("LastText = %q, want the most recent message's text (qué tal)", c.LastText)
	}
}

// TestConfigLevel covers every branch of ConfigLevel's priority-ordered
// mapping directly against the Chat struct — no store needed, it's a pure
// function.
// TestChatIsOff is T67 (ct-2026-08-11-172135) — the single definition of
// "apagado" now shared by send.go, resolve_chat, and (via offStatusSQL,
// pending.go's own SQL twin) every dispatch query. "new"/"whitelist" are the
// two statuses that survived T65 as meaningfully distinct from "off" — both
// must read as NOT off, same as any other non-ignored/blacklist value.
func TestChatIsOff(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"ignored", true},
		{"blacklist", true},
		{"new", false},
		{"whitelist", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			if got := ChatIsOff(tc.status); got != tc.want {
				t.Errorf("ChatIsOff(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestConfigLevel(t *testing.T) {
	cases := []struct {
		name string
		c    Chat
		want string
	}{
		{"is_boss wins over everything", Chat{IsBoss: true, Status: "ignored", Active: false, ConfirmationMode: "always"}, "boss"},
		{"ignored status", Chat{IsBoss: false, Status: "ignored", Active: true, ConfirmationMode: "none"}, "ignored"},
		{"inactive -> unattended", Chat{IsBoss: false, Status: "new", Active: false, ConfirmationMode: "none"}, "unattended"},
		{"confirmation always -> confirm", Chat{IsBoss: false, Status: "new", Active: true, ConfirmationMode: "always"}, "confirm"},
		{"confirmation discretion -> confirm", Chat{IsBoss: false, Status: "new", Active: true, ConfirmationMode: "discretion"}, "confirm"},
		{"confirmation none -> auto", Chat{IsBoss: false, Status: "new", Active: true, ConfirmationMode: "none"}, "auto"},
		{"unrecognized confirmation_mode -> unattended", Chat{IsBoss: false, Status: "new", Active: true, ConfirmationMode: "bogus"}, "unattended"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConfigLevel(tc.c); got != tc.want {
				t.Errorf("ConfigLevel(%+v) = %q, want %q", tc.c, got, tc.want)
			}
		})
	}
}

// TestSetConfigLevelRoundTrip covers each of the 5 levels: setting it
// persists the 4 fields as documented, and ConfigLevel reads the SAME level
// back. Starts from a fresh individual chat (status "new", TouchChat's
// default) for every case — the one edge case where round-trip doesn't
// hold (a chat already "ignored" before setting "confirm"/"boss") is
// flagged in SetConfigLevel's own doc comment, not exercised here.
func TestSetConfigLevelRoundTrip(t *testing.T) {
	for _, level := range []string{"boss", "auto", "confirm", "unattended", "ignored"} {
		t.Run(level, func(t *testing.T) {
			s := openTestStore(t)
			jid := "111@c.us"
			if err := s.TouchChat(jid, "Ana", 100); err != nil {
				t.Fatalf("TouchChat: %v", err)
			}
			if err := s.SetConfigLevel(jid, level); err != nil {
				t.Fatalf("SetConfigLevel(%q): %v", level, err)
			}
			c, ok, err := s.GetChat(jid)
			if err != nil || !ok {
				t.Fatalf("GetChat: ok=%v err=%v", ok, err)
			}
			if got := ConfigLevel(c); got != level {
				t.Errorf("ConfigLevel after SetConfigLevel(%q) = %q, want %q (chat=%+v)", level, got, level, c)
			}
		})
	}
}

// TestSetConfigLevelAutoPromotesIgnoredStatus covers the one status
// transition "auto" makes: an ignored (or blacklisted) chat becomes
// "whitelist" — SetConfigLevel's documented exception to "otherwise leave
// Status alone".
func TestSetConfigLevelAutoPromotesIgnoredStatus(t *testing.T) {
	s := openTestStore(t)
	jid := "222@g.us" // group default: status "ignored"
	if err := s.TouchChat(jid, "Grupo", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetConfigLevel(jid, "auto"); err != nil {
		t.Fatalf("SetConfigLevel(auto): %v", err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Status != "whitelist" {
		t.Errorf("Status after SetConfigLevel(auto) on an ignored chat = %q, want whitelist", c.Status)
	}
	if ConfigLevel(c) != "auto" {
		t.Errorf("ConfigLevel after SetConfigLevel(auto) = %q, want auto", ConfigLevel(c))
	}
}

// TestSetConfigLevelConfirmClearsIgnoredStatus covers T120's bug: a group
// is born with Status "ignored" (TouchChat's default). Choosing "confirm"
// from the dashboard must actually take — ConfigLevel must read back
// "confirm", not fall back to "ignored" because Status was never cleared.
func TestSetConfigLevelConfirmClearsIgnoredStatus(t *testing.T) {
	s := openTestStore(t)
	jid := "555@g.us" // group default: status "ignored"
	if err := s.TouchChat(jid, "Grupo", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetConfigLevel(jid, "confirm"); err != nil {
		t.Fatalf("SetConfigLevel(confirm): %v", err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Status != "whitelist" {
		t.Errorf("Status after SetConfigLevel(confirm) on an ignored chat = %q, want whitelist", c.Status)
	}
	if got := ConfigLevel(c); got != "confirm" {
		t.Errorf("ConfigLevel after SetConfigLevel(confirm) on an ignored chat = %q, want confirm (chat=%+v)", got, c)
	}
}

// TestSetConfigLevelBossClearsIgnoredStatus is the same bug, for "boss" —
// on a 1:1 chat (T121, ct-2026-09-02-1722, closed the "boss" level to
// groups entirely, so a group can no longer exercise this at all; see
// TestSetConfigLevelBossRejectsGroup). Checked on the raw Status field, not
// ConfigLevel: IsBoss outranks Status in ConfigLevel's switch, so
// ConfigLevel(c) already reads "boss" whether or not Status was cleared —
// that read-back can't catch this. The bug is the leftover Status itself,
// live the moment IsBoss reverts to false.
func TestSetConfigLevelBossClearsIgnoredStatus(t *testing.T) {
	s := openTestStore(t)
	jid := "555000031@s.whatsapp.net"
	if err := s.TouchChat(jid, "Persona", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetStatus(jid, "blacklist"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := s.SetConfigLevel(jid, "boss"); err != nil {
		t.Fatalf("SetConfigLevel(boss): %v", err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Status != "whitelist" {
		t.Errorf("Status after SetConfigLevel(boss) on a blacklisted chat = %q, want whitelist", c.Status)
	}
}

// TestSetConfigLevelUnattendedResetsIgnoredStatus covers "unattended"'s own
// status exception: an ignored chat reverts to "new" (same baseline
// TouchChat/handleSetIgnored already use to un-ignore).
func TestSetConfigLevelUnattendedResetsIgnoredStatus(t *testing.T) {
	s := openTestStore(t)
	jid := "333@c.us"
	if err := s.TouchChat(jid, "Bea", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetStatus(jid, "ignored"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := s.SetConfigLevel(jid, "unattended"); err != nil {
		t.Fatalf("SetConfigLevel(unattended): %v", err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Status != "new" {
		t.Errorf("Status after SetConfigLevel(unattended) on an ignored chat = %q, want new", c.Status)
	}
}

func TestSetConfigLevelRejectsInvalidLevel(t *testing.T) {
	s := openTestStore(t)
	jid := "444@c.us"
	if err := s.TouchChat(jid, "Cami", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetConfigLevel(jid, "bogus"); err == nil {
		t.Error("SetConfigLevel(bogus) = nil error, want an error")
	}
}

func TestClaimChatExclusive(t *testing.T) {
	s := openTestStore(t)
	if err := s.TouchChat("111@c.us", "Ana", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}

	ok, err := s.ClaimChat("111@c.us", "model-a", 60*time.Second)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	ok, err = s.ClaimChat("111@c.us", "model-b", 60*time.Second)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if ok {
		t.Error("model-b claimed a chat already held by model-a")
	}
	if err := s.ReleaseChat("111@c.us", "model-a"); err != nil {
		t.Fatalf("ReleaseChat: %v", err)
	}
	ok, err = s.ClaimChat("111@c.us", "model-b", 60*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim after release: ok=%v err=%v", ok, err)
	}
}

// TestNewChatDefaultsHistoryStatePending covers the migration's default
// (ct-2026-07-21-1306) — a brand-new chat, and by extension every
// pre-existing row backfilled by the ALTER, starts out "pending" so the
// history worker picks it up.
func TestNewChatDefaultsHistoryStatePending(t *testing.T) {
	s := openTestStore(t)
	if err := s.TouchChat("500@c.us", "Ana", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	c, ok, err := s.GetChat("500@c.us")
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.HistoryState != "pending" {
		t.Errorf("HistoryState = %q, want pending", c.HistoryState)
	}
}

// TestHistorySummaryCounts covers the post-ct-2026-07-24-2004 meaning: how
// many chats have at least one message on file, over the total — the
// ON_DEMAND worker's old history_state-based progress is gone (the worker
// itself was removed).
func TestHistorySummaryCounts(t *testing.T) {
	s := openTestStore(t)
	if err := s.TouchChat("506@c.us", "A", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.TouchChat("507@c.us", "B", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.TouchChat("508@c.us", "C", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.AddMessage(Message{ChatJID: "506@c.us", ID: "m1", Text: "hola", TS: 100}); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	withMessages, total, err := s.HistorySummary()
	if err != nil {
		t.Fatalf("HistorySummary: %v", err)
	}
	if withMessages != 1 || total != 3 {
		t.Errorf("HistorySummary = (%d, %d), want (1, 3)", withMessages, total)
	}
}

// ── M5 (ct-2026-07-22-1903) — defaults de atención por origen, pipeline ────

// configSource reads chats.config_level_source directly (white-box, same
// package) — not exposed on Chat itself, an internal bookkeeping column
// only applyOriginDefaultIfUnset needs to read.
func configSource(t *testing.T, s *Store, jid string) string {
	t.Helper()
	var v string
	if err := s.db.QueryRow(`SELECT config_level_source FROM chats WHERE jid = ?`, jid).Scan(&v); err != nil {
		t.Fatalf("configSource(%s): %v", jid, err)
	}
	return v
}

func TestTouchChatAppliesOriginDefaultForNewNumber(t *testing.T) {
	s := openTestStore(t)
	if err := s.KVSet(SettingConfigLevelDefaultNew, "auto"); err != nil {
		t.Fatal(err)
	}
	jid := "1@s.whatsapp.net"
	if err := s.TouchChat(jid, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if !c.Active || c.ConfirmationMode != "none" {
		t.Errorf("chat = %+v, want active=true confirmation_mode=none (the configured 'auto' default)", c)
	}
	if got := configSource(t, s, jid); got != "default" {
		t.Errorf("config_level_source = %q, want default", got)
	}
}

func TestTouchChatSkipsOriginDefaultForGroup(t *testing.T) {
	s := openTestStore(t)
	if err := s.KVSet(SettingConfigLevelDefaultNew, "auto"); err != nil {
		t.Fatal(err)
	}
	jid := "123-456@g.us"
	if err := s.TouchChat(jid, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	// Groups keep TouchChat's own baseline (ignored/always/active=false) —
	// the M5 origin axis must never touch a group row, even with a KV
	// default configured.
	if c.Active || c.Status != "ignored" || c.ConfirmationMode != "always" {
		t.Errorf("group chat = %+v, want TouchChat's unchanged group baseline", c)
	}
	if got := configSource(t, s, jid); got != "manual" {
		t.Errorf("group config_level_source = %q, want manual (groups are out of the origin axis)", got)
	}
}

func TestSetContactNameAppliesContactDefaultOverridingNewNumber(t *testing.T) {
	s := openTestStore(t)
	if err := s.KVSet(SettingConfigLevelDefaultNew, "unattended"); err != nil {
		t.Fatal(err)
	}
	if err := s.KVSet(SettingConfigLevelDefaultContact, "auto"); err != nil {
		t.Fatal(err)
	}
	jid := "2@s.whatsapp.net"
	if err := s.TouchChat(jid, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	c, _, _ := s.GetChat(jid)
	if c.Active {
		t.Fatalf("precondition: chat should start unattended (active=false), got %+v", c)
	}

	if err := s.SetContactName(jid, "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if !c.Active || c.ConfirmationMode != "none" {
		t.Errorf("chat after becoming a contact = %+v, want the 'auto' contact default (contact wins over new-number)", c)
	}
}

func TestApplyOriginDefaultRespectsManualOverride(t *testing.T) {
	s := openTestStore(t)
	if err := s.KVSet(SettingConfigLevelDefaultContact, "auto"); err != nil {
		t.Fatal(err)
	}
	jid := "3@s.whatsapp.net"
	if err := s.TouchChat(jid, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	// The owner explicitly sets confirmation_mode BEFORE the contact syncs —
	// a real decision that must survive the later contact-name transition.
	if err := s.SetConfirmationMode(jid, "always"); err != nil {
		t.Fatal(err)
	}
	if got := configSource(t, s, jid); got != "manual" {
		t.Fatalf("config_level_source after SetConfirmationMode = %q, want manual", got)
	}

	if err := s.SetContactName(jid, "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	c, _, _ := s.GetChat(jid)
	if c.ConfirmationMode != "always" {
		t.Errorf("ConfirmationMode = %q, want always — the owner's explicit choice must survive becoming a contact", c.ConfirmationMode)
	}
}

// TestApplyOriginDefaultFallsBackToUnattendedWhenKVUnset (ct-2026-07-22-2100,
// safety default: "por defecto el agente NO atiende, hasta que configure
// explícitamente" — boss verbatim): no config_level_default_* KV ever
// set still lands on the SAME unattended baseline TouchChat's own schema
// defaults already produce (active=false) — EffectiveConfigLevelDefault's
// "" -> "unattended" substitution is idempotent here, not a behavior
// change for a chat that was already going to be unattended anyway. The
// real effect is elsewhere (a chat that HAD an "auto" new-number default
// applied must fall back to unattended, not stay auto, once it becomes a
// contact with no contact-default configured — that's a separate risk this
// test doesn't need to re-cover, applyConfigLevelDefault's own unit
// behavior + TestSetContactNameAppliesContactDefaultOverridingNewNumber
// already establish the write path).
func TestApplyOriginDefaultFallsBackToUnattendedWhenKVUnset(t *testing.T) {
	s := openTestStore(t)
	// No config_level_default_* KV ever set — M5 untouched by the boss.
	jid := "4@s.whatsapp.net"
	if err := s.TouchChat(jid, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactName(jid, "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.Active || c.Status != "new" || c.ConfirmationMode != "none" {
		t.Errorf("chat = %+v, want unattended (active=false), status/confirmation_mode untouched by the 'unattended' case", c)
	}
}

// TestEffectiveConfigLevelDefault (ct-2026-07-22-2100): unset -> "unattended"
// (the safety default); an explicitly-configured value passes through
// unchanged.
func TestEffectiveConfigLevelDefault(t *testing.T) {
	s := openTestStore(t)
	got, err := s.EffectiveConfigLevelDefault(SettingConfigLevelDefaultNew)
	if err != nil || got != "unattended" {
		t.Errorf("EffectiveConfigLevelDefault(unset) = %q, err=%v, want unattended", got, err)
	}
	if err := s.KVSet(SettingConfigLevelDefaultNew, "auto"); err != nil {
		t.Fatal(err)
	}
	got, err = s.EffectiveConfigLevelDefault(SettingConfigLevelDefaultNew)
	if err != nil || got != "auto" {
		t.Errorf("EffectiveConfigLevelDefault(set to auto) = %q, err=%v, want auto", got, err)
	}
}

// TestEffectiveRulesOriginAxis (M5, ct-2026-07-22-1903): for an individual
// chat with no particular rules, contact vs new-number decides which
// origin tier applies — contact WINS (boss verbatim). Groups are
// unaffected, still rules_type_group. T79 (ct-2026-08-27-2034) removed the
// global-default tier below rules_type_group — a group with no type rule
// now resolves to "" ("sin rules no actúa"), same as any other chat with
// nothing set at its own tier; there's no further fallback left to test.
func TestEffectiveRulesOriginAxis(t *testing.T) {
	s := openTestStore(t)
	if err := s.KVSet(SettingRulesDefaultNewNumber, "número nuevo: cauteloso"); err != nil {
		t.Fatal(err)
	}
	if err := s.KVSet(SettingRulesDefaultContact, "contacto: cálido"); err != nil {
		t.Fatal(err)
	}

	newNumber := "10@s.whatsapp.net"
	if err := s.TouchChat(newNumber, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveRules(newNumber); err != nil || got != "número nuevo: cauteloso" {
		t.Errorf("EffectiveRules(new number) = %q, err=%v, want the new-number origin tier", got, err)
	}

	contact := "11@s.whatsapp.net"
	if err := s.TouchChat(contact, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactName(contact, "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveRules(contact); err != nil || got != "contacto: cálido" {
		t.Errorf("EffectiveRules(contact) = %q, err=%v, want the contact origin tier — contact must WIN", got, err)
	}

	// A particular rule on the chat itself still outranks the origin tier.
	if err := s.SetChatRules(contact, "particular: mío"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveRules(contact); err != nil || got != "particular: mío" {
		t.Errorf("EffectiveRules(contact with particular rules) = %q, err=%v, want the particular rule", got, err)
	}

	// Groups: unaffected by the origin axis, still rules_type_group -> "".
	group := "12345@g.us"
	if err := s.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveRules(group); err != nil || got != "" {
		t.Errorf("EffectiveRules(group, no type rules) = %q, err=%v, want \"\" — no global tier left to fall back to (T79)", got, err)
	}
	if err := s.SetTypeRules("grupo: tipo"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveRules(group); err != nil || got != "grupo: tipo" {
		t.Errorf("EffectiveRules(group with type rules) = %q, err=%v, want the group type tier", got, err)
	}
}

// TestEffectiveAgentDefaultOriginAxis (T71, ct-2026-08-27-1410): same
// contact-wins-over-new-number precedence as EffectiveRules, but for the
// default dispatch agent instead of rules text — and no "general" tier of
// its own (an unset origin key returns "", the caller's own PortFallback
// fallback covers "general"). Groups always return "".
func TestEffectiveAgentDefaultOriginAxis(t *testing.T) {
	s := openTestStore(t)
	if err := s.KVSet(SettingAgentDefaultNewNumber, "term-new"); err != nil {
		t.Fatal(err)
	}
	if err := s.KVSet(SettingAgentDefaultContact, "term-contact"); err != nil {
		t.Fatal(err)
	}
	if err := s.KVSet(SettingAgentDefaultGroup, "term-group"); err != nil {
		t.Fatal(err)
	}
	if err := s.KVSet(SettingAgentDefaultBoss, "term-boss"); err != nil {
		t.Fatal(err)
	}

	newNumber := "20@s.whatsapp.net"
	if err := s.TouchChat(newNumber, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveAgentDefault(newNumber); err != nil || got != "term-new" {
		t.Errorf("EffectiveAgentDefault(new number) = %q, err=%v, want term-new", got, err)
	}

	contact := "21@s.whatsapp.net"
	if err := s.TouchChat(contact, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactName(contact, "Juan Pérez"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveAgentDefault(contact); err != nil || got != "term-contact" {
		t.Errorf("EffectiveAgentDefault(contact) = %q, err=%v, want term-contact — contact must WIN", got, err)
	}

	// T72 (ct-2026-08-27-1625): group is now its own type, same shape
	// EffectiveRules already uses for group rules.
	group := "22345@g.us"
	if err := s.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveAgentDefault(group); err != nil || got != "term-group" {
		t.Errorf("EffectiveAgentDefault(group) = %q, err=%v, want term-group", got, err)
	}

	// T72: boss is the most specific type — wins even over a chat that's
	// ALSO a known contact (is_boss and contact_name aren't mutually
	// exclusive in the schema, boss must still win).
	boss := "23@s.whatsapp.net"
	if err := s.TouchChat(boss, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContactName(boss, "El Dueño"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIsBoss(boss, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveAgentDefault(boss); err != nil || got != "term-boss" {
		t.Errorf("EffectiveAgentDefault(boss) = %q, err=%v, want term-boss — boss must WIN over contact", got, err)
	}
}

// TestEffectiveAgentDefaultUnsetReturnsEmpty (T71, group/boss added T72):
// no origin key configured for ANY of the 4 types -> "" — the caller's
// PortFallback covers "general", there is no agent-default-general KV key
// to seed for any type.
func TestEffectiveAgentDefaultUnsetReturnsEmpty(t *testing.T) {
	s := openTestStore(t)
	jid := "24@s.whatsapp.net"
	if err := s.TouchChat(jid, "Alguien", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveAgentDefault(jid); err != nil || got != "" {
		t.Errorf("EffectiveAgentDefault(nothing configured) = %q, err=%v, want \"\"", got, err)
	}

	group := "25345@g.us"
	if err := s.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveAgentDefault(group); err != nil || got != "" {
		t.Errorf("EffectiveAgentDefault(group, nothing configured) = %q, err=%v, want \"\"", got, err)
	}

	boss := "26@s.whatsapp.net"
	if err := s.TouchChat(boss, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIsBoss(boss, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EffectiveAgentDefault(boss); err != nil || got != "" {
		t.Errorf("EffectiveAgentDefault(boss, nothing configured) = %q, err=%v, want \"\" — Parte C: sin configurar, PortFallback (el principal) sigue atendiendo", got, err)
	}
}

func TestMarkConfigManualBlocksFutureOriginDefault(t *testing.T) {
	s := openTestStore(t)
	if err := s.KVSet(SettingConfigLevelDefaultNew, "auto"); err != nil {
		t.Fatal(err)
	}
	jid := "5@s.whatsapp.net"
	if err := s.TouchChat(jid, "Alguien", 1); err != nil { // config_level_source starts 'default'
		t.Fatal(err)
	}
	if got := configSource(t, s, jid); got != "default" {
		t.Fatalf("precondition: config_level_source = %q, want default", got)
	}

	// Simulates handleSetIgnored's own pair of calls (SetStatus + MarkConfigManual).
	if err := s.SetStatus(jid, "ignored"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConfigManual(jid); err != nil {
		t.Fatal(err)
	}
	if got := configSource(t, s, jid); got != "manual" {
		t.Fatalf("config_level_source after MarkConfigManual = %q, want manual", got)
	}

	// A later TouchChat (another inbound message) must NOT revive it via
	// the origin default — the owner's ignore decision wins.
	if err := s.TouchChat(jid, "Alguien", 2); err != nil {
		t.Fatal(err)
	}
	c, _, _ := s.GetChat(jid)
	if c.Status != "ignored" {
		t.Errorf("Status = %q, want ignored to survive TouchChat", c.Status)
	}
}

// TestSetIsApproverRoundTrip (Aprobador P1, ct-2026-07-31-0610): the pin
// round-trips independently of is_boss/config_level_source — orthogonal by
// construction, see Chat.IsApprover's doc.
func TestSetIsApproverRoundTrip(t *testing.T) {
	s := openTestStore(t)
	jid := "222@c.us"
	if err := s.TouchChat(jid, "Secretaria", 1); err != nil {
		t.Fatal(err)
	}

	if err := s.SetIsApprover(jid, true); err != nil {
		t.Fatalf("SetIsApprover(true): %v", err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if !c.IsApprover {
		t.Error("IsApprover = false after SetIsApprover(true)")
	}
	if c.IsBoss {
		t.Error("IsBoss = true after SetIsApprover(true) — the pin must not imply ownership")
	}

	if err := s.SetIsApprover(jid, false); err != nil {
		t.Fatalf("SetIsApprover(false): %v", err)
	}
	c, _, _ = s.GetChat(jid)
	if c.IsApprover {
		t.Error("IsApprover = true after SetIsApprover(false)")
	}
}

// TestSetIsApproverIndependentOfConfigLevel: marking a chat approver must
// not look like a manual config-level override — is_approver is a separate
// axis from the 5 unified config levels (boss/auto/confirm/unattended/
// ignored), unlike SetIsBoss/SetActive/SetConfirmationMode which all mark
// config_level_source='manual' because they DO set one of those 5.
func TestSetIsApproverIndependentOfConfigLevel(t *testing.T) {
	s := openTestStore(t)
	jid := "223@c.us"
	if err := s.TouchChat(jid, "Auto", 1); err != nil {
		t.Fatal(err)
	}
	before := configSource(t, s, jid)

	if err := s.SetIsApprover(jid, true); err != nil {
		t.Fatal(err)
	}
	if got := configSource(t, s, jid); got != before {
		t.Errorf("config_level_source after SetIsApprover = %q, want unchanged from %q — the pin is orthogonal to config_level", got, before)
	}
}

// TestMarkOwnerIfUntouchedMarksFreshChat is the T12 (ct-2026-08-05-1231)
// clean-install case: a chat nobody has ever decided is_boss for gets
// marked owner the first time MarkOwnerIfUntouched runs (whatsmeow's
// recordOwnIdentity, at connect).
// TestSetIsBossRejectsGroupJID covers T121: boss identifies a person by
// number — a group is not a person, so SetIsBoss must refuse to mark one,
// unconditionally, at the one write path every "level=boss" caller
// (REST admin, SetConfigLevel) funnels through. Rejects BEFORE writing —
// the group must stay unmentioned in chats, not merely un-boss'd.
func TestSetIsBossRejectsGroupJID(t *testing.T) {
	s := openTestStore(t)
	jid := "555100000001@g.us"
	if err := s.SetIsBoss(jid, true); err == nil {
		t.Fatal("SetIsBoss(group, true) = nil error, want a rejection — a group cannot be boss")
	}
	if _, ok, err := s.GetChat(jid); err != nil {
		t.Fatalf("GetChat: %v", err)
	} else if ok {
		t.Error("SetIsBoss(group, true) wrote a chat row despite rejecting the change")
	}
}

// TestSetIsBossAllowsUnmarkingGroup: the guard only blocks MARKING a group
// boss — unmarking (isBoss=false) must never be refused, or a cleanup call
// would get stuck against the very thing it's trying to undo.
func TestSetIsBossAllowsUnmarkingGroup(t *testing.T) {
	s := openTestStore(t)
	jid := "555100000002@g.us"
	if err := s.TouchChat(jid, "Grupo", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetIsBoss(jid, false); err != nil {
		t.Fatalf("SetIsBoss(group, false) = %v, want nil — unmarking must never be blocked", err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.IsBoss {
		t.Error("IsBoss = true after SetIsBoss(group, false), want false")
	}
}

// TestSetIsBossStillAllowsIndividualChat: the guard is group-specific — a
// 1:1 chat must keep working exactly as before.
func TestSetIsBossStillAllowsIndividualChat(t *testing.T) {
	s := openTestStore(t)
	jid := "555000030@s.whatsapp.net"
	if err := s.TouchChat(jid, "Persona", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetIsBoss(jid, true); err != nil {
		t.Fatalf("SetIsBoss(1:1, true) = %v, want nil", err)
	}
	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if !c.IsBoss {
		t.Error("IsBoss = false after SetIsBoss(1:1, true), want true")
	}
}

// TestSetConfigLevelBossRejectsGroup confirms SetConfigLevel needs no guard
// of its own — it funnels level=boss through SetIsBoss, so T121's gate
// there is enough to reject this path too.
func TestSetConfigLevelBossRejectsGroup(t *testing.T) {
	s := openTestStore(t)
	jid := "555100000003@g.us"
	if err := s.TouchChat(jid, "Grupo", 100); err != nil {
		t.Fatalf("TouchChat: %v", err)
	}
	if err := s.SetConfigLevel(jid, "boss"); err == nil {
		t.Fatal("SetConfigLevel(group, boss) = nil error, want a rejection")
	}
}

func TestMarkOwnerIfUntouchedMarksFreshChat(t *testing.T) {
	s := openTestStore(t)
	jid := "55500000021@s.whatsapp.net"
	if err := s.TouchChat(jid, "Yo", 1); err != nil {
		t.Fatal(err)
	}

	if err := s.MarkOwnerIfUntouched(jid); err != nil {
		t.Fatalf("MarkOwnerIfUntouched: %v", err)
	}

	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if !c.IsBoss {
		t.Error("IsBoss = false after MarkOwnerIfUntouched on a fresh chat, want true")
	}
	if c.ConfirmationMode != "none" {
		t.Errorf("ConfirmationMode = %q, want none — TouchChat's own individual-chat default must survive, or every reply to the owner becomes a draft awaiting his own approval (send.go's confirmation_mode gate ignores is_boss)", c.ConfirmationMode)
	}
}

// TestMarkOwnerIfUntouchedSkipsManualUnmark is the T12 reconnect case: once
// the owner has explicitly unmarked his own chat (SetIsBoss(false), via the
// REST admin path), a later reconnect (MarkOwnerIfUntouched again) must
// never re-apply the auto-mark — that would fight the owner's own decision.
func TestMarkOwnerIfUntouchedSkipsManualUnmark(t *testing.T) {
	s := openTestStore(t)
	jid := "55500000021@s.whatsapp.net"
	if err := s.TouchChat(jid, "Yo", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOwnerIfUntouched(jid); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIsBoss(jid, false); err != nil {
		t.Fatalf("SetIsBoss(false): %v", err)
	}

	// Simulates a reconnect (*events.Connected fires again).
	if err := s.MarkOwnerIfUntouched(jid); err != nil {
		t.Fatalf("MarkOwnerIfUntouched (reconnect): %v", err)
	}

	c, ok, err := s.GetChat(jid)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if c.IsBoss {
		t.Error("IsBoss = true after a reconnect following a manual unmark — must stay false, the owner's decision must not be fought")
	}
}

// TestMarkOwnerIfUntouchedNoopOnUntouchedRow: a chat row that pre-dates T12
// (is_boss_touched defaults to 1 on migration, see schema.go) must never be
// silently flipped to owner just because it happens to be OwnJID one day —
// MarkOwnerIfUntouched only ever fires its effect on a genuinely untouched
// (is_boss_touched=0) row.
func TestMarkOwnerIfUntouchedNoopOnUntouchedRow(t *testing.T) {
	s := openTestStore(t)
	jid := "55500000074@s.whatsapp.net"
	if err := s.TouchChat(jid, "Otro", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE chats SET is_boss_touched = 1 WHERE jid = ?`, jid); err != nil {
		t.Fatal(err)
	}

	if err := s.MarkOwnerIfUntouched(jid); err != nil {
		t.Fatal(err)
	}

	c, _, _ := s.GetChat(jid)
	if c.IsBoss {
		t.Error("IsBoss = true after MarkOwnerIfUntouched on an already-touched row, want it left alone")
	}
}

// ── ChatOrigin (T18, ct-2026-08-05-1243) ────────────────────────────────

// TestChatOriginRealMessageEitherDirection is T18's core regression:
// ChatOrigin used to check ONLY from_me=0 (inbound) — a chat the OWNER
// started that never got a reply read as group_discovered/synced_contact
// instead of the real conversation it is. Both directions must now count.
func TestChatOriginRealMessageEitherDirection(t *testing.T) {
	s := openTestStore(t)

	t.Run("owner-initiated, no reply, also a group member", func(t *testing.T) {
		jid := "111@c.us"
		if err := s.TouchChat(jid, "A", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: true, Text: "hola, tenes stock?", TS: 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertGroupMember("group1@g.us", jid, "", 1); err != nil {
			t.Fatal(err)
		}
		origin, err := s.ChatOrigin(jid)
		if err != nil {
			t.Fatal(err)
		}
		if origin != "inbound_spoke" {
			t.Errorf("ChatOrigin = %q, want inbound_spoke (a real outbound-only conversation, even though the contact shares a group)", origin)
		}
	})

	t.Run("owner-initiated, no reply, no group", func(t *testing.T) {
		jid := "222@c.us"
		if err := s.TouchChat(jid, "B", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: true, Text: "hola, tenes stock?", TS: 1}); err != nil {
			t.Fatal(err)
		}
		origin, err := s.ChatOrigin(jid)
		if err != nil {
			t.Fatal(err)
		}
		if origin != "inbound_spoke" {
			t.Errorf("ChatOrigin = %q, want inbound_spoke", origin)
		}
	})

	t.Run("real inbound reply exists (control)", func(t *testing.T) {
		jid := "333@c.us"
		if err := s.TouchChat(jid, "C", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.AddMessage(Message{ChatJID: jid, ID: "m1", FromMe: true, Text: "hola", TS: 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.AddMessage(Message{ChatJID: jid, ID: "m2", FromMe: false, Text: "hola, si tenemos", TS: 2}); err != nil {
			t.Fatal(err)
		}
		origin, err := s.ChatOrigin(jid)
		if err != nil {
			t.Fatal(err)
		}
		if origin != "inbound_spoke" {
			t.Errorf("ChatOrigin = %q, want inbound_spoke", origin)
		}
	})
}

// TestChatOriginGroupDiscoveredAndSyncedContact: no real message anywhere —
// the two remaining cases split on group_members membership.
func TestChatOriginGroupDiscoveredAndSyncedContact(t *testing.T) {
	s := openTestStore(t)

	groupMember := "444@c.us"
	if err := s.TouchChat(groupMember, "D", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember("group2@g.us", groupMember, "", 1); err != nil {
		t.Fatal(err)
	}
	if origin, err := s.ChatOrigin(groupMember); err != nil || origin != "group_discovered" {
		t.Errorf("ChatOrigin(no message, in a group) = %q, err=%v, want group_discovered", origin, err)
	}

	plain := "555@c.us"
	if err := s.TouchChat(plain, "E", 1); err != nil {
		t.Fatal(err)
	}
	if origin, err := s.ChatOrigin(plain); err != nil || origin != "synced_contact" {
		t.Errorf("ChatOrigin(no message, no group) = %q, err=%v, want synced_contact", origin, err)
	}
}

// TestChatOriginIgnoresProtocolNoise: a `messages` row with no real text/type
// (a delivery receipt or reaction WhatsApp still persists as a row) must not
// count as a real conversation — same realMessageSQL criterion
// ChatJIDsWithMessages already uses, reused here on purpose (T18) so the two
// can't disagree about whether a chat "really happened".
func TestChatOriginIgnoresProtocolNoise(t *testing.T) {
	s := openTestStore(t)
	jid := "666@c.us"
	if err := s.TouchChat(jid, "F", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroupMember("group3@g.us", jid, "", 1); err != nil {
		t.Fatal(err)
	}
	// Protocol noise: both Text and Type empty — realMessageSQL excludes it.
	if err := s.AddMessage(Message{ChatJID: jid, ID: "noise1", FromMe: false, Text: "", Type: "", TS: 1}); err != nil {
		t.Fatal(err)
	}

	origin, err := s.ChatOrigin(jid)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "group_discovered" {
		t.Errorf("ChatOrigin with only a content-less message row = %q, want group_discovered (protocol noise isn't a conversation)", origin)
	}
}

// TestBossJIDsDeduplicatesDeviceSuffix is T52's primary case
// (ct-2026-08-10-1837): a ghost row with device suffix and the real row for
// the same number are the same destination — BossJIDs must return one, not
// two, or every send_to_boss / recovery code produces a visible duplicate.
func TestBossJIDsDeduplicatesDeviceSuffix(t *testing.T) {
	s := openTestStore(t)

	const ghost = "555000001:15@s.whatsapp.net"
	const real = "555000001@s.whatsapp.net"

	if err := s.SetIsBoss(ghost, true); err != nil {
		t.Fatalf("SetIsBoss ghost: %v", err)
	}
	if err := s.SetIsBoss(real, true); err != nil {
		t.Fatalf("SetIsBoss real: %v", err)
	}

	jids, err := s.BossJIDs()
	if err != nil {
		t.Fatalf("BossJIDs: %v", err)
	}
	if len(jids) != 1 {
		t.Fatalf("BossJIDs returned %d entries, want 1: %v", len(jids), jids)
	}
	if jids[0] != real {
		t.Errorf("BossJIDs[0] = %q, want %q", jids[0], real)
	}
}

// TestBossJIDsPreservesDistinctNumbers verifies that deduplication only
// collapses entries for the same underlying number, not different ones.
func TestBossJIDsPreservesDistinctNumbers(t *testing.T) {
	s := openTestStore(t)

	const a = "555000001@s.whatsapp.net"
	const b = "555000002@s.whatsapp.net"

	if err := s.SetIsBoss(a, true); err != nil {
		t.Fatalf("SetIsBoss a: %v", err)
	}
	if err := s.SetIsBoss(b, true); err != nil {
		t.Fatalf("SetIsBoss b: %v", err)
	}

	jids, err := s.BossJIDs()
	if err != nil {
		t.Fatalf("BossJIDs: %v", err)
	}
	if len(jids) != 2 {
		t.Fatalf("BossJIDs returned %d entries, want 2: %v", len(jids), jids)
	}
}

// ── T125 (ct-2026-09-02-2221) — every chats write normalizes the JID ────
//
// How this happened: chat.go had ~18 raw INSERT/UPDATE statements against
// `chats`, and only TouchChat stripped WhatsApp's own-device suffix
// (":<NN>", T45) before writing. Any of the other 17 — SetIsBoss among
// them — called once with a device-suffixed jid created a SECOND, parallel
// row for the same real chat, silently: no error, just two chats. That's
// exactly what happened in the owner's real installation: two rows marked
// is_boss, and send_to_boss wrote three real notifications in August to
// the ghost — none of them ever arrived. The retry/dead-letter machinery
// worked correctly; the destination itself was wrong from the moment it
// was enqueued.
//
// The fix (chat.go): `jid = StripDeviceSuffix(jid)` as the first line of
// every function whose body writes to `chats` — not a shared exec
// wrapper. Considered one (a single low-level helper every INSERT routes
// through): it works cleanly for the INSERT class, where jid is uniformly
// the first bound parameter, but SEVERAL functions use jid in more than
// one place before that final write — SetActive reads `active` and calls
// MarkHandledBefore(jid, ...) BEFORE its own upsert; SetConfigLevel calls
// four other setters AND clearIgnoredStatus's own GetChat read. A wrapper
// around just the terminal db.Exec call would leave those earlier uses
// unprotected, so the per-function guard was needed regardless — and once
// it's there, a second wrapper adds indirection without adding a
// correctness guarantee the guard doesn't already provide.

// TestChatWriteSettersNormalizeJID is the "guard against the ninth
// setter" this contract asks for, not a fixed list of today's offenders:
// it parses chat.go's OWN SOURCE at test time and, for every function
// whose body contains a literal "INSERT INTO chats" or "UPDATE chats",
// requires that SAME function's body to also call StripDeviceSuffix.
// Adding a setter #19 later that writes to `chats` without normalizing —
// exactly how this bug happened the first time — fails THIS test in red,
// instead of silently creating another ghost row. A table of today's
// functions couldn't do that: it only catches what someone remembered to
// add to it.
func TestChatWriteSettersNormalizeJID(t *testing.T) {
	src, err := os.ReadFile("chat.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "chat.go", src, 0)
	if err != nil {
		t.Fatalf("parsing chat.go: %v", err)
	}

	// exempt: functions that legitimately write to `chats` WITHOUT
	// normalizing jid, and why. Keep this list to one entry unless a new,
	// equally-deliberate reason shows up — anyone adding to it has to
	// justify it right here, in the same place this test lives.
	exempt := map[string]string{
		"MergeDeviceSuffixedOwnChat": "its entire job is repairing rows " +
			"ALREADY dirty with a device suffix (found via a LIKE query on " +
			"the raw, unstripped form) — normalizing its own jid would make " +
			"it target nothing and defeat the repair. See its own doc comment.",
	}

	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv == nil {
			continue // free functions (IsGroupJID, StripDeviceSuffix itself, ...) never write
		}
		start := fset.Position(fn.Body.Pos()).Offset
		end := fset.Position(fn.Body.End()).Offset
		body := string(src[start:end])

		writesChats := strings.Contains(body, "INSERT INTO chats") || strings.Contains(body, "UPDATE chats")
		if !writesChats {
			continue
		}
		if _, ok := exempt[fn.Name.Name]; ok {
			continue
		}
		if !strings.Contains(body, "StripDeviceSuffix(") {
			violations = append(violations, fn.Name.Name)
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("these functions write to `chats` (INSERT/UPDATE) without ever calling StripDeviceSuffix on the jid — a device-suffixed JID (\"usuario:NN@s.whatsapp.net\") creates a ghost row instead of updating the real one (T125, ct-2026-09-02-2221): %s\n\nfix: add `jid = StripDeviceSuffix(jid)` as the function's first line — or, if this write is deliberately meant to target an already-dirty jid, add it to this test's exempt map with the same justification MergeDeviceSuffixedOwnChat has.", strings.Join(violations, ", "))
	}
}

// TestSetterWithDeviceSuffixWritesNormalizedRowNotAGhost is the
// behavioral half: calls EVERY current chats-writing setter with a
// synthetic 555 JID carrying a device suffix, and checks the write landed
// on the ONE normalized row — never a second, parallel one. This is what
// proves TODAY's 17 fixed functions are actually correct, not just that
// they happen to mention StripDeviceSuffix somewhere (the static test
// above) — table-driven so adding function #18 tomorrow is one more row,
// not a new test.
func TestSetterWithDeviceSuffixWritesNormalizedRowNotAGhost(t *testing.T) {
	const clean = "555000090@s.whatsapp.net"
	const dirty = "555000090:7@s.whatsapp.net" // same account, "device 7"

	cases := []struct {
		name  string
		apply func(s *Store) error
		check func(t *testing.T, c Chat)
	}{
		{"SetMode", func(s *Store) error { return s.SetMode(dirty, "dedicated") },
			func(t *testing.T, c Chat) { wantEq(t, "Mode", c.Mode, "dedicated") }},
		{"SyncRouterMode", func(s *Store) error { return s.SyncRouterMode(dirty, "auto") },
			func(t *testing.T, c Chat) { wantEq(t, "Mode", c.Mode, "auto") }},
		{"SetActive", func(s *Store) error { return s.SetActive(dirty, true) },
			func(t *testing.T, c Chat) { wantEq(t, "Active", c.Active, true) }},
		{"SetArchived", func(s *Store) error { return s.SetArchived(dirty, true) },
			func(t *testing.T, c Chat) { wantEq(t, "Archived", c.Archived, true) }},
		{"SetStatus", func(s *Store) error { return s.SetStatus(dirty, "whitelist") },
			func(t *testing.T, c Chat) { wantEq(t, "Status", c.Status, "whitelist") }},
		{"SetIsBoss", func(s *Store) error { return s.SetIsBoss(dirty, true) },
			func(t *testing.T, c Chat) { wantEq(t, "IsBoss", c.IsBoss, true) }},
		{"MarkOwnerIfUntouched", func(s *Store) error { return s.MarkOwnerIfUntouched(dirty) },
			func(t *testing.T, c Chat) { wantEq(t, "IsBoss", c.IsBoss, true) }},
		{"SetIsApprover", func(s *Store) error { return s.SetIsApprover(dirty, true) },
			func(t *testing.T, c Chat) { wantEq(t, "IsApprover", c.IsApprover, true) }},
		{"SetChatMemory", func(s *Store) error { return s.SetChatMemory(dirty, "le gusta el mate") },
			func(t *testing.T, c Chat) { wantEq(t, "Memory", c.Memory, "le gusta el mate") }},
		{"SetChatContext", func(s *Store) error { return s.SetChatContext(dirty, "cliente frecuente") },
			func(t *testing.T, c Chat) { wantEq(t, "Context", c.Context, "cliente frecuente") }},
		{"SetChatSilence", func(s *Store) error { return s.SetChatSilence(dirty, "ya contestó el dueño", 100) },
			func(t *testing.T, c Chat) { wantEq(t, "SilenceReason", c.SilenceReason, "ya contestó el dueño") }},
		{"SetChatRules", func(s *Store) error { return s.SetChatRules(dirty, "sé breve") },
			func(t *testing.T, c Chat) { wantEq(t, "Rules", c.Rules, "sé breve") }},
		{"SetConfirmationMode", func(s *Store) error { return s.SetConfirmationMode(dirty, "always") },
			func(t *testing.T, c Chat) { wantEq(t, "ConfirmationMode", c.ConfirmationMode, "always") }},
		{"SetConfirmer", func(s *Store) error { return s.SetConfirmer(dirty, "555000091@s.whatsapp.net") },
			func(t *testing.T, c Chat) { wantEq(t, "Confirmer", c.Confirmer, "555000091@s.whatsapp.net") }},
		{"SetChatDescription", func(s *Store) error { return s.SetChatDescription(dirty, "tema del grupo") },
			func(t *testing.T, c Chat) { wantEq(t, "Description", c.Description, "tema del grupo") }},
		{"SetGroupInviteLink", func(s *Store) error { return s.SetGroupInviteLink(dirty, "https://chat.whatsapp.com/x") },
			func(t *testing.T, c Chat) { wantEq(t, "GroupInviteLink", c.GroupInviteLink, "https://chat.whatsapp.com/x") }},
		{"SetContactName", func(s *Store) error { return s.SetContactName(dirty, "Contacto Uno") },
			func(t *testing.T, c Chat) { wantEq(t, "ContactName", c.ContactName, "Contacto Uno") }},
		{"MarkConfigManual", func(s *Store) error { return s.MarkConfigManual(dirty) },
			func(t *testing.T, c Chat) { wantEq(t, "ConfigLevelSource", c.ConfigLevelSource, "manual") }},
		{"SetConfigLevel", func(s *Store) error { return s.SetConfigLevel(dirty, "confirm") },
			func(t *testing.T, c Chat) { wantEq(t, "ConfigLevel", ConfigLevel(c), "confirm") }},
		{"ClaimChat", func(s *Store) error { _, err := s.ClaimChat(dirty, "gpt-5", time.Minute); return err },
			func(t *testing.T, c Chat) { wantEq(t, "ClaimedBy", c.ClaimedBy, "gpt-5") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			if err := s.TouchChat(dirty, "C", 1); err != nil {
				t.Fatalf("setup TouchChat: %v", err)
			}

			if err := tc.apply(s); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}

			if _, ok, err := s.GetChat(dirty); err != nil {
				t.Fatalf("GetChat(dirty): %v", err)
			} else if ok {
				t.Errorf("%s left a GHOST row under the device-suffixed JID %q — this is exactly T125's bug", tc.name, dirty)
			}

			c, ok, err := s.GetChat(clean)
			if err != nil {
				t.Fatalf("GetChat(clean): %v", err)
			}
			if !ok {
				t.Fatalf("%s: no row under the normalized JID %q — the write went nowhere findable", tc.name, clean)
			}
			tc.check(t, c)
		})
	}
}

// wantEq is a tiny generic helper for the table above — one line per case
// instead of a type-switch, comparable values only (every field the table
// checks is a string/bool).
func wantEq[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

// TestReleaseChatNormalizesJID and TestSetActiveSweepsBeforeFlippingWithDeviceSuffix
// cover the two setters TestSetterWithDeviceSuffixWritesNormalizedRowNotAGhost's
// simple table can't express cleanly — ReleaseChat needs a PRIOR claim to
// release, and SetActive's anti-avalanche sweep (S8) needs a real pending
// message to sweep — both still exercised with a device-suffixed JID.

// TestReleaseChatNormalizesJID: a claim made (however it happened to be
// claimed) is released by the SAME dirty JID a caller might reasonably use
// — must clear the real, normalized row, not silently no-op against a jid
// that was never claimed under that exact spelling.
func TestReleaseChatNormalizesJID(t *testing.T) {
	s := openTestStore(t)
	const dirty = "555000091:3@s.whatsapp.net"
	const clean = "555000091@s.whatsapp.net"
	if err := s.TouchChat(dirty, "C", 1); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimChat(dirty, "gpt-5", time.Minute); err != nil || !ok {
		t.Fatalf("setup ClaimChat: ok=%v err=%v", ok, err)
	}

	if err := s.ReleaseChat(dirty, "gpt-5"); err != nil {
		t.Fatalf("ReleaseChat: %v", err)
	}

	c, ok, err := s.GetChat(clean)
	if err != nil || !ok {
		t.Fatalf("GetChat(clean): ok=%v err=%v", ok, err)
	}
	if c.ClaimedBy != "" {
		t.Errorf("ClaimedBy after ReleaseChat = %q, want empty — release must clear the real row", c.ClaimedBy)
	}
}

// TestSetActiveSweepsBeforeFlippingWithDeviceSuffix: S8's anti-avalanche
// sweep (SetActive, chat.go) reads `active` and marks old messages handled
// BEFORE the upsert — both steps have to use the SAME normalized jid, or
// the sweep silently misses (looks at a row that was never inactive to
// begin with) while the flip itself still lands correctly, defeating S8
// for exactly the callers most likely to pass a raw, unnormalized JID.
func TestSetActiveSweepsBeforeFlippingWithDeviceSuffix(t *testing.T) {
	s := openTestStore(t)
	const dirty = "555000092:1@s.whatsapp.net"
	const clean = "555000092@s.whatsapp.net"
	if err := s.TouchChat(dirty, "C", 1); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * activationSweepWindow).Unix()
	if err := s.AddMessage(Message{ChatJID: dirty, ID: "old1", FromMe: false, Text: "viejo", TS: old}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetActive(dirty, true); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	due, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range due {
		if m.ChatJID == clean && m.ID == "old1" {
			t.Errorf("PendingDedicated still returns the stale message after SetActive(dirty, true) — the anti-avalanche sweep (S8) missed it because it read the wrong (unnormalized) row")
		}
	}
}
