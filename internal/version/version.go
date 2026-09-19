// Package version expone la versión única del gateway. La fuente es el
// archivo VERSION en la raíz del repo — nunca se escribe el número a mano
// en otro lugar. build-all.sh la inyecta en el binario por -ldflags -X;
// esa vía gana siempre que esté presente.
//
// go:embed no puede leer un archivo fuera del directorio del paquete
// (restricción del compilador, no un atajo elegido acá), así que este
// paquete embebe una copia local (VERSION, en este mismo directorio) que
// build-all.sh resincroniza desde la raíz antes de cada build — esa copia
// es el default para un "go build ./..." plano, sin -ldflags, para que
// nunca quede vacío.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var embedded string

// Version es la versión del gateway. build-all.sh la fija por
// -ldflags -X piumy-gateway/internal/version.Version=<contenido de
// VERSION> — el enlazador la pisa ANTES de que corra init(), por eso
// queda sin inicializar acá (var, no const): init() solo completa el
// valor cuando -ldflags no la tocó.
var Version string

func init() {
	if Version == "" {
		Version = strings.TrimSpace(embedded)
	}
}
