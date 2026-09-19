// El freno de reconexión anti-ban (T99, ct-2026-08-29-1607) — repone lo
// que el Piumy viejo tenía (core/internal/gateway/gateway.go:370-410) y se
// perdió en el pivote a whatsmeow, pero con un comportamiento distinto: el
// viejo, a los MaxFails=5, DEJABA DE REINTENTAR y esperaba una acción del
// dueño. Acá no — decisión del dueño, verbatim: "la reconexion debe ser
// automatica, no martillante constante". Nunca se rinde; el delay entre
// intentos crece con los fallos consecutivos, con jitter, hasta un techo.
//
// El porqué de tomar el control en vez de ajustar los parámetros que ya
// trae whatsmeow: la librería resetea su propio contador de fallos
// (AutoReconnectErrors) a 0 en CADA handshake exitoso
// (connectionevents.go:165, handleConnectSuccess) — aunque esa conexión
// dure dos segundos. Un canal que conecta y se cae en ciclo reintenta con
// delay ~0 para siempre; es el patrón que le costó a OpenClaw 3.500 ciclos
// de conexión en 3 horas y una restricción de 72+ horas sobre la cuenta
// (issue #16270, citado en el contrato). AutoReconnectHook (client.go:649)
// no sirve de enganche para esto: solo se llama cuando el intento de
// conectar FALLA — en el ciclo de martilleo el handshake tiene éxito (por
// eso resetea el contador) y cae después, así que el hook nunca se
// dispara. Verificado línea por línea contra el whatsmeow vendored
// (v0.0.0-20260806224404-e277b766ab33) antes de escribir esto, no asumido.
//
// Por eso New() apaga client.EnableAutoReconnect y este archivo pasa a
// manejar el reintento entero — con SU PROPIO contador, que solo se
// resetea después de que una conexión se sostiene reconnectStableAfter
// (armReconnectStableTimer, disparado desde handleConnected en CADA
// handshake exitoso) — no en el primer handshake, que es exactamente el
// bug de arriba. Verificado también que LoggedOut/StreamReplaced/
// ClientOutdated/device_removed ya llaman a expectDisconnect() por su
// cuenta en la librería y nunca dependieron de EnableAutoReconnect para
// quedar terminales — apagarlo no les cambia nada.
package whatsmeow

import (
	"context"
	"log"
	"math/rand"
	"time"
)

// reconnectJitter es la fracción ± aplicada a cada delay calculado —
// anti-ban del proyecto: ninguna espera fija ni redonda contra WhatsApp.
// Constante interna, no config (Citrino, T99): es CÓMO randomizar, no una
// perilla de producto.
const reconnectJitter = 0.20

// jitteredBackoff calcula 5s (o base) × 2^(failures-1), con techo max, y le
// aplica reconnectJitter. Misma forma que corepipeline/outbox.go's
// exponentialBackoff (mismo base 5s) — el techo es distinto a propósito:
// el del outbox (1h) es "cuánto esperar antes de rendirse"; este freno
// nunca se rinde, así que su techo es la espera máxima PERMANENTE una vez
// agotado el crecimiento — con 1h, la red del dueño podría volver y Piumy
// tardar hasta una hora en enterarse, lo que se siente muerto, no
// automático (Citrino). Con 5 minutos (el default) son ~12 intentos por
// hora en el peor caso — el caso de OpenClaw eran ~19 por MINUTO: tres
// órdenes de magnitud de margen.
//
// failures siempre >= 1 cuando se llama (mismo invariante que
// exponentialBackoff). Pura — sin estado, sin reloj real — así que se
// prueba exhaustivamente sin mocks.
func jitteredBackoff(failures int, base, max time.Duration) time.Duration {
	if base <= 0 {
		base = 5 * time.Second
	}
	if max <= 0 || max < base {
		max = 5 * time.Minute
	}
	backoff := base
	for i := 1; i < failures; i++ {
		backoff *= 2
		if backoff > max {
			backoff = max
			break
		}
	}
	//nolint:gosec // no-crypto random a propósito — timing anti-ban, no seguridad
	spread := float64(backoff) * reconnectJitter
	jittered := float64(backoff) + (rand.Float64()*2*spread - spread)
	if jittered < 0 {
		return backoff
	}
	return time.Duration(jittered)
}

// scheduleReconnect es el reemplazo propio del auto-reconnect de whatsmeow
// (apagado en New() — ver el doc del package arriba). Cancela cualquier
// timer de estabilidad pendiente (una caída nueva significa que la
// conexión anterior NO se sostuvo), suma un fallo consecutivo propio, y
// agenda un intento con jitteredBackoff.
//
// Fail-open por construcción (requisito explícito de Citrino, T99): dado un
// connectFn real, la ÚNICA forma de que esto deje de reintentar es la
// cancelación de ctx (Stop()/apagado del proceso) — no hay ninguna rama
// que devuelva sin haber programado el siguiente intento. reconnect_test.go
// lo prueba con un connectFn que falla siempre: la cuenta de llamadas sigue
// subiendo, nunca se frena sola. connectFn nil (un Adapter armado a mano
// para un test, sin pasar por New()) es la ÚNICA excepción — mismo
// convenio nil-safe que Store/Router/Bus en este mismo archivo: nada que
// reintentar, así que no agenda nada, en vez de reventar en la goroutine
// de reconnectAfter cuando el timer dispare.
func (a *Adapter) scheduleReconnect(ctx context.Context) {
	if a.connectFn == nil {
		return
	}
	a.reconnectMu.Lock()
	a.reconnectGen++
	if a.reconnectStableTimer != nil {
		a.reconnectStableTimer.Stop()
		a.reconnectStableTimer = nil
	}
	a.reconnectFailures++
	failures := a.reconnectFailures
	a.reconnectMu.Unlock()

	delay := jitteredBackoff(failures, a.reconnectBaseDelay, a.reconnectMaxDelay)
	log.Printf("whatsmeow: reconectando en %s (intento consecutivo %d)", delay.Round(time.Second), failures)

	go a.reconnectAfter(ctx, delay)
}

// connectOrScheduleRetry intenta UN connect inmediato (el primero, al
// arrancar con una sesión ya pareada — Start) y, si falla, lo entrega al
// mismo mecanismo que cualquier caída posterior (scheduleReconnect) en vez
// de propagar el error.
//
// SIEMPRE devuelve nil. Antes de T99, un fallo acá volvía tal cual hasta
// main.go's log.Fatalf — corepipeline.Controller.Start trata cualquier
// error de gw.Start como fatal y NUNCA levanta el pipeline/MCP/REST (ver
// controller.go). El caso típico es la máquina que arranca antes de que la
// red levante (el dueño la prende todos los días, según Citrino) — un
// problema transitorio de WhatsApp no puede tirar abajo el proceso
// ENTERO, exactamente la misma razón de ser de Start's propio doc
// ("no bloquear, no tirar abajo MCP/REST"), aplicada al primer intento y
// no solo a una caída posterior.
func (a *Adapter) connectOrScheduleRetry(ctx context.Context) error {
	if err := a.connectFn(); err != nil {
		log.Printf("whatsmeow: connect (sesión existente): %v — reintentando en segundo plano", err)
		a.scheduleReconnect(ctx)
	}
	return nil
}

// reconnectAfter espera delay (o la cancelación de ctx) y dispara UN
// intento vía connectFn (a.client.Connect en producción — ver Adapter.
// connectFn's doc). connectFn devolviendo error significa que el intento
// falló sincrónicamente (red inalcanzable, etc.) — se reagenda de
// inmediato con scheduleReconnect, que suma otro fallo y recalcula el
// delay. connectFn devolviendo nil solo significa que el handshake
// arrancó: si termina en éxito real, *events.Connected dispara
// armReconnectStableTimer (inbound.go); si esa misma conexión se cae de
// nuevo, *events.Disconnected vuelve a entrar acá por el camino normal
// (handleEvent) — ningún caso queda sin una próxima jugada.
func (a *Adapter) reconnectAfter(ctx context.Context, delay time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	if err := a.connectFn(); err != nil {
		log.Printf("whatsmeow: intento de reconexión falló: %v", err)
		a.scheduleReconnect(ctx)
	}
}

// armReconnectStableTimer arranca (o reinicia) la ventana que una conexión
// tiene que sobrevivir antes de que scheduleReconnect's propio contador de
// fallos se resetee a 0 — el corazón del contrato: whatsmeow resetea el
// SUYO en el primer handshake exitoso, sin importar cuánto dure; este
// reset solo pasa tras reconnectStableAfter de conexión sostenida.
// Llamado desde handleConnected en CADA handshake exitoso, incluido uno
// que está por caerse de nuevo — si un *events.Disconnected nuevo llega
// antes de que este timer dispare, scheduleReconnect lo cancela, así que
// un ciclo de martilleo nunca llega a resetear nada.
//
// reconnectGen (mismo idioma que state.Manager.reactGen) cierra la carrera
// donde el timer dispara justo en el mismo instante en que llega una
// caída nueva: solo el timer armado en la generación VIGENTE puede
// resetear el contador — uno viejo que ya perdió la carrera contra un
// scheduleReconnect concurrente se lee a sí mismo obsoleto y no hace nada.
func (a *Adapter) armReconnectStableTimer() {
	a.reconnectMu.Lock()
	defer a.reconnectMu.Unlock()
	if a.reconnectStableTimer != nil {
		a.reconnectStableTimer.Stop()
	}
	a.reconnectGen++
	myGen := a.reconnectGen
	a.reconnectStableTimer = time.AfterFunc(a.reconnectStableAfter, func() {
		a.reconnectMu.Lock()
		defer a.reconnectMu.Unlock()
		if a.reconnectGen != myGen {
			return
		}
		if a.reconnectFailures > 0 {
			log.Printf("whatsmeow: conexión sostenida %s — contador de reintentos reseteado (venía de %d fallos consecutivos)", a.reconnectStableAfter, a.reconnectFailures)
		}
		a.reconnectFailures = 0
		a.reconnectStableTimer = nil
	})
}
