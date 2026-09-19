package i18n

import "testing"

func TestParseLangPrefix(t *testing.T) {
	cases := map[string]Lang{
		"es-AR":       ES,
		"es_ES.UTF-8": ES,
		"en-US":       EN,
		"en_US.UTF-8": EN,
		"EN-GB":       EN,
		"pt-BR":       ES, // no soportado todavía -> español
		"":            ES,
	}
	for in, want := range cases {
		if got := parseLangPrefix(in); got != want {
			t.Errorf("parseLangPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValid(t *testing.T) {
	if !Valid(ES) || !Valid(EN) {
		t.Fatal("ES y EN deben ser válidos")
	}
	if Valid(Lang("fr")) {
		t.Fatal("fr no debería ser válido todavía")
	}
}

func TestCatalogFallsBackToSpanish(t *testing.T) {
	if c := Catalog(Lang("fr")); c == nil {
		t.Fatal("Catalog debería devolver el mapa de español para un idioma desconocido, no nil")
	}
}

func TestDetectNeverPanics(t *testing.T) {
	lang := Detect()
	if lang != ES && lang != EN {
		t.Fatalf("Detect() devolvió un idioma no soportado: %q", lang)
	}
}

func TestResolve(t *testing.T) {
	if got := Resolve("en"); got != EN {
		t.Errorf("Resolve(%q) = %q, want %q — la elección manual gana", "en", got, EN)
	}
	if got := Resolve("es"); got != ES {
		t.Errorf("Resolve(%q) = %q, want %q — la elección manual gana", "es", got, ES)
	}
	if got := Resolve(""); got != Detect() {
		t.Errorf("Resolve(\"\") = %q, want Detect() = %q — nunca elegido, cae al locale", got, Detect())
	}
	if got := Resolve("fr"); got != Detect() {
		t.Errorf("Resolve(%q) = %q, want Detect() = %q — idioma que Piumy no ofrece, cae al locale", "fr", got, Detect())
	}
}

func TestT(t *testing.T) {
	if got := T(ES, "server.agent_unreachable"); got != "agente sin conexión" {
		t.Errorf("T(ES, server.agent_unreachable) = %q, want %q", got, "agente sin conexión")
	}
	want := "🔐 Código de recuperación del dashboard Piumy: 123456 — vence en 10 minutos, un solo uso."
	if got := T(ES, "server.recovery_code", "code", "123456"); got != want {
		t.Errorf("T con hueco {code} = %q, want %q", got, want)
	}
	if got := T(ES, "no.existe"); got != "[no.existe]" {
		t.Errorf("T con clave desconocida = %q, want %q — roto y visible, no roto y callado", got, "[no.existe]")
	}
}
