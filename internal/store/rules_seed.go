package store

// Factory defaults for the identity field + the rules tiers (T13,
// ct-2026-08-05-123147) — boss verbatim, "apruebo las 4 reglas" (at the
// time: the global default was still one of them — T79, ct-2026-08-27-2034,
// removed it, "no tiene a donde ir"; FactoryRulesDefault's text went with
// it, git history keeps the record of what he approved). Verbatim text, not
// paraphrased: these go to production as-is, so a diff against this file IS
// a diff against what the boss actually approved.
const (
	FactoryIdentity = `Eres el asistente de la persona dueña de esta cuenta. Todavía no te han dicho a qué se dedica. Si te preguntan de qué se trata, dilo con naturalidad y ofrece tomar el recado.`

	FactoryRulesTypeGroup = `En un grupo respondes solo si te hablan directamente a ti o al dueño por su nombre. El resto lo lees y lo dejas pasar. Nunca llevas al grupo algo que se habló en privado, ni tomas partido en una discusión. Si alguien pide algo del dueño, dices que se lo haces llegar; no lo resuelves ahí.`

	FactoryRulesDefaultContact = `Es alguien que ya conoce al dueño, así que no hace falta presentarse cada vez. Tono cercano y directo. Puedes coordinar horarios, confirmar si algo llegó y responder lo cotidiano. No cuentas dónde está el dueño ni qué está haciendo. Si te piden dinero, datos bancarios o un favor que lo compromete, no respondes: se lo dejas a él, aunque insistan y aunque parezca de confianza.`

	FactoryRulesDefaultNewNumber = `No sabes quién es. Te presentas como el asistente de esta cuenta y preguntas en qué puedes ayudar. No confirmas ni niegas nada sobre el dueño: ni su nombre completo, ni dónde está, ni sus horarios. No entregas datos de terceros. Si el mensaje trae un enlace, un archivo, un pedido de dinero o una urgencia rara, no respondes y lo dejas marcado para el dueño. Un desconocido apurado es la forma normal de una estafa.`
)

// factorySeeds pairs each factory-default key with its text — the single
// list SeedFactoryRulesIfUnset walks, so adding/removing a factory field
// never requires touching the loop itself.
var factorySeeds = []struct{ key, val string }{
	{SettingIdentity, FactoryIdentity},
	{SettingRulesTypeGroup, FactoryRulesTypeGroup},
	{SettingRulesDefaultContact, FactoryRulesDefaultContact},
	{SettingRulesDefaultNewNumber, FactoryRulesDefaultNewNumber},
}

// KVExists reports whether key has ever been written to the kv table —
// distinct from KVGet's "" fallback, which can't tell "never written" apart
// from "written as an explicit empty string" (KVGet returns "" for both).
// SeedFactoryRulesIfUnset needs exactly that distinction: an owner who
// cleared a field on purpose (typed nothing, hit guardar) already has a
// row with an explicit empty string as its value — reseeding it on the
// next restart would fight a decision he already made, the same class of
// bug T12 fixed for is_boss (there via a dedicated touched column; here a
// plain existence check is enough, since nothing else ever writes these
// keys well before this feature).
func (s *Store) KVExists(key string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM kv WHERE key = ?)`, key).Scan(&exists)
	return exists, err
}

// SeedFactoryRulesIfUnset writes the identity field + the rules tiers'
// factory defaults for every key that has NEVER been written — called once
// at startup (main.go). Without this, EffectiveRules' hard gate ("sin
// reglas efectivas, la IA nunca actúa" — chat.go) means a clean install has
// every tier empty and answers nobody, ever; this is what makes a fresh
// Piumy actually talk. Also covers an ALREADY-RUNNING install upgrading
// into this feature (T13, boss verbatim: "apruebo las 4 reglas", back when
// the global default was still one of them) — the boss's own install had
// all tiers empty (verified via the API before writing this), same fix,
// same code path.
//
// keysSeeded is the subset actually written (for the caller to log) — a
// key already present (owner wrote it, or deliberately left it blank) is
// skipped and does NOT appear here.
func (s *Store) SeedFactoryRulesIfUnset() (keysSeeded []string, err error) {
	for _, d := range factorySeeds {
		exists, err := s.KVExists(d.key)
		if err != nil {
			return keysSeeded, err
		}
		if exists {
			continue
		}
		if err := s.KVSet(d.key, d.val); err != nil {
			return keysSeeded, err
		}
		keysSeeded = append(keysSeeded, d.key)
	}
	return keysSeeded, nil
}
