package i18n

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestNoUntranslatedProseInKnownSinks (T153 etapa 2b-v, ct-2026-09-08-1656):
// TestAllKeysMatchBetweenFrontendAndCatalog (catalog_usage_test.go) proves
// every key someone ASKS FOR exists — it says nothing about text that never
// asked for a key at all. Citrino's own manual sweep found several (the QR
// overlay, agent-card labels, "Guardar") that lived outside every single
// etapa's stated scope, precisely because the etapas were cut BY AREA
// (chats, agentes, login...) and an area list can't prove nothing is left
// over — only a sweep over the whole file, by PATTERN, can. This test is
// that sweep, made permanent.
//
// It scans app.js for literal strings sitting in a fixed list of KNOWN
// SINKS — places whose value is known to reach the user unconditionally,
// enumerated below in sinkPatterns — and fails if any such literal contains
// real text (see looksLikeProse) that isn't a call to t(...) and isn't on
// the explicit exception list (allowedNonProseLiterals). Two design points
// carried over from Citrino's own post-mortem on his two prior scripts:
//
//  1. It must see backtick templates and short, unaccented words — his v1
//     missed both, which is exactly how "Guardar" and "Conectando…" got
//     past it. sinkLiteralPattern below matches double/single/backtick
//     quotes alike, and looksLikeProse never requires an accent.
//  2. The margin for what legitimately ISN'T translated (a URL example, a
//     comparison identifier, a console log) has to be a literal, visible,
//     commented list — never a side effect of a clever regex. That list is
//     allowedNonProseLiterals below; anything not covered by a SINK pattern
//     in the first place (console.*, className, getElementById, comparison
//     values like `source === "tipo:grupo"`) needs no entry there at all —
//     it was never a sink to begin with.
//
// What this test deliberately does NOT scan, and why: LEVELS/
// AGENT_DEFAULT_TYPES/HISTORY_BADGES keep their ORIGINAL text as
// documentation + fallback inside object/array literals (label/short/
// title fields) — verified by hand, not by this test, that every one of
// those fields is read exclusively through a named resolver function
// (levelLabel/originLevelShort/agentTypeLabel/historyBadgeTitle) that
// returns a real t(...) value for every key except the ones truly meant to
// stay the same word in both languages (e.g. "Boss"). A resolver's OWN
// if-chain (t("literal.key")) is what TestAllKeysMatchBetweenFrontendAndCatalog
// already audits; the array's raw text is inert data feeding it, not a
// sink. Adding a NEW array of this shape means re-doing that manual check,
// not adding it to sinkPatterns.
func TestNoUntranslatedProseInKnownSinks(t *testing.T) {
	data, err := os.ReadFile("../dashboard/web/app.js")
	if err != nil {
		t.Fatalf("leyendo app.js: %v", err)
	}
	src := string(data)

	for _, sp := range sinkPatterns {
		for _, m := range sp.re.FindAllStringSubmatchIndex(src, -1) {
			expr := src[m[2]:m[3]]
			for _, lit := range extractBareLiterals(expr) {
				if !looksLikeProse(lit) {
					continue
				}
				if _, ok := allowedNonProseLiterals[lit]; ok {
					continue
				}
				line := 1 + strings.Count(src[:m[2]], "\n")
				t.Errorf("app.js:~%d: %s tiene un literal sin traducir: %q — pasalo por t(\"clave\") o, si de verdad no es traducible, sumalo a allowedNonProseLiterals con el motivo", line, sp.name, lit)
			}
		}
	}
}

// sinkPatterns: los lugares donde un string, una vez asignado, llega a
// pantalla sin ninguna transformación más — la lista declarada que
// reemplaza cualquier heurística implícita sobre "qué es una asignación
// visible". Cada entrada captura, en el grupo 1, la expresión completa del
// lado derecho (o del argumento) hasta el límite natural de la
// instrucción — de ahí se extraen los literales sueltos.
var sinkPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"asignación .textContent/.title/.innerHTML/.alt/.placeholder/.value",
		regexp.MustCompile(`\.(?:textContent|title|innerHTML|alt|placeholder|value)\s*=\s*([^;]+);`)},
	{`setAttribute("title"/"placeholder"/"alt"/"data-label", ...)`,
		regexp.MustCompile(`setAttribute\(\s*"(?:title|placeholder|alt|data-label)"\s*,\s*([^;]+);`)},
	{"agentInput(container, LABEL, ...) — 2do argumento",
		regexp.MustCompile(`agentInput\([^,]+,\s*([^,]+),`)},
	{"sectionHeader(TEXTO)",
		regexp.MustCompile(`sectionHeader\(([^)]+)\)`)},
	{"contactRow(NOMBRE, ...) — 1er argumento",
		regexp.MustCompile(`contactRow\(([^,]+),`)},
	{`objeto opts de agentInput: campo "placeholder"/"hint"`,
		regexp.MustCompile(`\b(?:placeholder|hint)\s*:\s*([^,}]+)[,}]`)},
}

// sinkLiteralPattern encuentra un literal entre comillas (dobles, simples o
// invertidas) DENTRO de la expresión ya recortada por un sinkPattern,
// capturando además el contexto que lo descarta como texto final:
//   - precedido por "t(" (grupo 1): es la CLAVE del catálogo, no el texto
//     — ese cruce ya lo hace TestAllKeysMatchBetweenFrontendAndCatalog.
//   - pegado a un operador de comparación antes o después (grupos 2 y 6,
//     `===`/`!==`/`==`/`!=`): es un VALOR que se compara contra un dato
//     (`a.role === "principal"`, `data.status === "updated"`), nunca lo
//     que termina en pantalla — la etiqueta visible de esos casos siempre
//     pasa por t() en la otra rama del propio ternario.
var sinkLiteralPattern = regexp.MustCompile(
	"(t\\(\\s*)?" +
		"(===|!==|==|!=)?\\s*" +
		"(?:\"((?:[^\"\\\\]|\\\\.)*)\"|'((?:[^'\\\\]|\\\\.)*)'|`([^`]*)`)" +
		"\\s*(===|!==|==|!=)?",
)

// stripUnicodeEscapes: "🟢" (el emoji 🟢 escrito como escape JS,
// no como bytes UTF-8 crudos) contiene letras latinas ("u","d","f"...) que
// son parte de la SINTAXIS del escape, no del texto — sin esto,
// looksLikeProse los confunde con prosa.
var unicodeEscapePattern = regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)

func stripUnicodeEscapes(s string) string {
	return unicodeEscapePattern.ReplaceAllString(s, "")
}

func extractBareLiterals(expr string) []string {
	var out []string
	for _, m := range sinkLiteralPattern.FindAllStringSubmatch(expr, -1) {
		if m[1] != "" {
			continue // t("clave literal", ...) — la clave, no el texto final.
		}
		if m[2] != "" || m[6] != "" {
			continue // pegado a === / !== / == / != — valor de comparación, no texto final.
		}
		for _, g := range m[3:6] {
			if g != "" {
				out = append(out, stripUnicodeEscapes(g))
				break
			}
		}
	}
	return out
}

// looksLikeProse: deliberadamente LAXO — cualquier literal de 2+
// caracteres con al menos una letra latina cuenta como candidato. Un
// literal de 0-1 caracteres nunca es una frase (cubre el `""`/`"s"` que la
// pluralización {n}/{s} deja como literal suelto en el propio código
// fuente, ver group.member_count/draft.pending_count). Ancho a propósito:
// la letra corta y sin acento es EXACTAMENTE lo que la v1 del script de
// Citrino no veía, y es lo que dejó pasar "Guardar".
func looksLikeProse(s string) bool {
	if len([]rune(s)) < 2 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
		if r >= 0x00C0 && r <= 0x024F { // Latin-1 Supplement + Latin Extended-A (áéíóúñ y mayúsculas)
			return true
		}
	}
	return false
}

// allowedNonProseLiterals: el margen declarado que pidió Citrino — cada
// entrada es un literal exacto que SÍ cae dentro de un sinkPattern de
// arriba pero NO es prosa de interfaz, con el motivo escrito al lado.
// Agregar una excepción acá es una decisión que se justifica, no un efecto
// lateral del regex.
var allowedNonProseLiterals = map[string]string{
	"http://192.168.1.10:8787": "ejemplo de URL en un placeholder — una dirección IP:puerto no cambia con el idioma",
	// "Piumy Gateway"/"Piumy Gateway — " (S3, ct-2026-09-20-1202,
	// applyAccountIdentity → document.title): el nombre del producto NUNCA
	// se traduce (T153 etapa 3c, misma regla que tray_windows.go's
	// SetTitle/SetTooltip en el lado Go) — el segundo literal es el prefijo
	// suelto que queda al concatenar "+ account" (un dato, tampoco
	// traducible), no una frase a medio armar.
	"Piumy Gateway":    "nombre del producto — nunca se traduce, misma regla que el lado Go (tray_windows.go)",
	"Piumy Gateway — ": "prefijo del título de pestaña con cuenta — el nombre del producto no se traduce, el nombre de cuenta que sigue es un dato",
}

// TestAllowedNonProseLiteralsHaveReasons: guarda contra una excepción
// agregada sin explicar por qué — la disciplina que el propio Citrino pidió
// ("declarado y a la vista"), hecha test en vez de quedar solo en la
// convención.
func TestAllowedNonProseLiteralsHaveReasons(t *testing.T) {
	for lit, reason := range allowedNonProseLiterals {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("allowedNonProseLiterals[%q] no tiene motivo — cada excepción se justifica", lit)
		}
	}
}
