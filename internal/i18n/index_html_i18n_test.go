package i18n

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// visibleTextTagPattern finds an opening tag from a whitelist of elements
// that render their own inline text, immediately followed by a run of
// plain text — either a real leaf's whole content ("<span>piumy</span>")
// or the leading text node before a nested child ("<span>WhatsApp <b
// id=...>"), both shapes exist throughout index.html. Captures: 1=tag
// name (unused, kept for readability), 2=attributes, 3=the text run.
var visibleTextTagPattern = regexp.MustCompile(`<(div|button|span|label|p|h[1-6]|a|th|td|legend|option)\b([^>]*)>([^<]+)`)

// i18nAttrPattern finds title=/placeholder=/alt=/aria-label= attribute
// VALUES on any tag — the four the project already has a data-i18n-*
// counterpart for (see applyI18n() in app.js).
var i18nAttrPattern = regexp.MustCompile(`\b(title|placeholder|alt|aria-label)="([^"]*)"`)

// htmlCommentPattern strips <!-- ... --> blocks before the scan — this
// file's own comments cite example markup inline ("puesto en el <button>,
// borraría el <span id=...>"), which visibleTextTagPattern/i18nAttrPattern
// would otherwise read as real elements.
var htmlCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)

// looksLikeIndexProse mirrors prose_sink_test.go's own looksLikeProse
// (app.js side): real text has at least one letter (plain or the accented
// Spanish ones this catalog uses) and isn't just punctuation, a symbol, or
// a lone digit.
func looksLikeIndexProse(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || strings.ContainsRune("áéíóúñÁÉÍÓÚÑ", r) {
			return true
		}
	}
	return false
}

// indexHTMLI18nExceptions (T165, ct-2026-09-16-2016) — every visible-text
// spot in index.html that deliberately has NO data-i18n(-title/-placeholder/
// -alt/-aria-label) binding, with why. This guard exists because the first
// three (TestAllKeysMatchBetweenFrontendAndCatalog, TestNoUntranslatedProseInKnownSinks,
// TestServerKeysMatchGoCallSites) only verify what ALREADY carries a
// data-i18n binding — none of them can see the ABSENCE of one.
// #configbtn's bare "Config ⚙" (found by T164's visual pass, fixed in this
// same commit) was exactly that: not a one-off slip, a whole class of gap.
var indexHTMLI18nExceptions = map[string]string{
	"piumy": "el nombre del producto en minúscula estilizada (topbar), o el valor " +
		"literal de la contraseña de fábrica usado como placeholder de ejemplo " +
		"(login) — ninguno de los dos es prosa traducible.",
	"WhatsApp": "nombre de marca de la plataforma de mensajería, no se traduce (misma " +
		"clase que GitHub/Reddit/clever.cat más abajo).",
	"GitHub": "nombre de marca — link del footer.",
	"Reddit": "nombre de marca — link del footer.",
	"BOSS": "término ya establecido para el chat del dueño — el propio CHANGELOG " +
		"documenta que se sacó \"Jefe\" a propósito (0.9.8: \"sin Jefe\"); no es un " +
		"olvido de T153.",
	"Boss": "mismo término que BOSS arriba, usado en minúscula/mayúscula-inicial en " +
		"la fila de Reglas — misma decisión ya tomada, no de T153.",
	"Governor (anti-ban)": "\"Governor\" y \"anti-ban\" ya son préstamos ingleses " +
		"establecidos en este tablero (ver el comentario de badge.governor_killed " +
		"en catalog.go: \"kill\"/\"Governor\"/\"Sync\" de la etapa 2a) — mismo criterio " +
		"que el badge homónimo del status-bar.",
	"Email": "préstamo inglés ya establecido para un label de un solo campo, mismo " +
		"criterio que agent.label_endpoint/label_pin (\"Endpoint\"/\"PIN\", ver su " +
		"propio comentario en catalog.go).",
	"Español": "autónimo a propósito — el comentario junto al <select> ya lo explica: " +
		"cada idioma se muestra en sí mismo, convención universal de selectores de " +
		"idioma, para que quien abrió el tablero en un idioma que no lee pueda " +
		"encontrar el suyo igual.",
	"English": "mismo motivo que \"Español\" arriba — autónimo a propósito.",
	"idle": "HALLAZGO SIN CERRAR, no una decisión: app.js:264 hace " +
		"moodlabel.textContent = mood con el valor CRUDO del backend " +
		"(state.Status.Mood: idle/qr/responding/error/sleeping/muted/alert...), " +
		"sin pasar por i18n.T() ni t() en ningún punto. \"alive\" (topbar, sin id, " +
		"nunca tocado por JS) tiene el mismo problema. Necesitaría su propio " +
		"mapeo enum→clave, mismo molde que levelLabel/agentTypeLabel — no lo " +
		"arreglo acá para no sumar alcance por mi cuenta (T165 no lo pidió); " +
		"reportado a Citrino en el cierre de este contrato.",
	"0 pendientes":      "placeholder que reescribe JS al vuelo con t(\"draft.pending_count\", ...) — el literal estático solo se ve un instante antes de que loadPendingDrafts() corra.",
	"Editar":            "placeholder que reescribe JS con t(\"action.edit\") + el nombre del chat (dato) — ver openEditModal.",
	"Editar borrador":   "placeholder que reescribe JS con t(\"action.edit\") + el nombre del chat (dato) — ver openDraftEditModal.",
	"Rechazar borrador": "placeholder que reescribe JS con t(\"action.reject\") + el nombre del chat (dato) — ver openDraftRejectModal.",
	"Conecta tu WhatsApp": "placeholder que reescribe JS con t(\"hero.name_disconnected\")/" +
		"t(\"hero.name_connected\", ...) en cuanto loadStatus() corre — ver renderHeroIdentity.",
	"Escanea el QR para vincular tu cuenta al gateway": "placeholder que reescribe JS " +
		"con t(\"hero.number_disconnected\")/el número real en cuanto loadStatus() corre.",
	"Escanea con WhatsApp → Dispositivos vinculados": "placeholder que reescribe JS " +
		"con t(\"qr.scan_instructions\") en cuanto el flujo de QR arranca.",
	"Buscar…": "placeholder que reescribe JS con applySearchPlaceholder() (T165, " +
		"ver app.js) en cuanto el catálogo carga — antes de T165 este era " +
		"justamente el hallazgo 3 de la mirada visual de T164.",
}

// TestIndexHTMLElementsHaveI18n is T165's own guard (ct-2026-09-16-2016) —
// barre TODO index.html por PATRÓN (qué tags llevan texto visible, qué
// atributos son traducibles), no por una lista de elementos armada de
// memoria: mismo diagnóstico que ya nos costó dos huecos (el área de QR en
// 2b-v, la bandeja y el email en 3c/3d) — enumerar por lista deja huecos,
// enumerar por patrón no. Falla si un elemento con texto visible real no
// tiene data-i18n (o -title/-placeholder/-alt/-aria-label según el
// atributo) NI está declarado en indexHTMLI18nExceptions con motivo.
func TestIndexHTMLElementsHaveI18n(t *testing.T) {
	data, err := os.ReadFile("../dashboard/web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	bodyIdx := strings.Index(string(data), "<body>")
	if bodyIdx < 0 {
		t.Fatal("no se encontró <body> en index.html — ¿cambió la estructura del archivo?")
	}
	// Los comentarios HTML de este archivo citan markup de ejemplo en su
	// prosa ("puesto en el <button>, borraría el <span id=...>") — sacarlos
	// ANTES de barrer, o esos ejemplos se leen como elementos reales.
	body := htmlCommentPattern.ReplaceAllString(string(data)[bodyIdx:], "")

	found := 0
	for _, m := range visibleTextTagPattern.FindAllStringSubmatch(body, -1) {
		attrs, text := m[2], m[3]
		if strings.Contains(attrs, "data-i18n") {
			continue
		}
		val := strings.TrimSpace(text)
		if !looksLikeIndexProse(val) {
			continue
		}
		found++
		if _, ok := indexHTMLI18nExceptions[val]; !ok {
			t.Errorf("texto visible %q sin data-i18n y sin declarar en indexHTMLI18nExceptions — clasificalo (T165, ct-2026-09-16-2016)", val)
		}
	}

	tagPattern := regexp.MustCompile(`<[a-zA-Z][^>]*>`)
	for _, tag := range tagPattern.FindAllString(body, -1) {
		for _, am := range i18nAttrPattern.FindAllStringSubmatch(tag, -1) {
			attrName, val := am[1], strings.TrimSpace(am[2])
			if !looksLikeIndexProse(val) {
				continue
			}
			if strings.Contains(tag, "data-i18n-"+attrName) {
				continue
			}
			found++
			if _, ok := indexHTMLI18nExceptions[val]; !ok {
				t.Errorf("atributo %s=%q sin data-i18n-%s y sin declarar en indexHTMLI18nExceptions — clasificalo (T165, ct-2026-09-16-2016)", attrName, val, attrName)
			}
		}
	}

	if found == 0 {
		t.Fatal("ningún texto visible encontrado en index.html — el patrón o el path están rotos, no el HTML")
	}
}

// TestIndexHTMLI18nExceptionsHaveReasons — misma disciplina que
// TestErrorLiteralExceptionsHaveReasons/TestAllowedNonProseLiteralsHaveReasons:
// una excepción con motivo vacío es indistinguible de una que nadie miró.
func TestIndexHTMLI18nExceptionsHaveReasons(t *testing.T) {
	for val, reason := range indexHTMLI18nExceptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("indexHTMLI18nExceptions[%q] no tiene motivo escrito", val)
		}
	}
}
