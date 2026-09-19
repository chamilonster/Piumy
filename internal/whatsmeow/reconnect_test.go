// T99 (ct-2026-08-29-1607): el freno de reconexión — ver reconnect.go's
// package doc para el diseño completo. Tests pedidos explícitamente por
// Citrino: fail-open (2) y el arranque sin red (3); más jitteredBackoff
// (pura, sin mocks) y el reset-solo-tras-conexión-sostenida, que es el
// corazón del contrato.
package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// TestJitteredBackoffGrowsAndCaps: pura, sin reloj real — failures=1 está
// en [base*(1-jitter), base*(1+jitter)], crece con cada fallo adicional, y
// nunca pasa max*(1+jitter) una vez alcanzado el techo.
func TestJitteredBackoffGrowsAndCaps(t *testing.T) {
	base := 5 * time.Second
	max := 5 * time.Minute

	d1 := jitteredBackoff(1, base, max)
	if lo, hi := time.Duration(float64(base)*0.8), time.Duration(float64(base)*1.2); d1 < lo || d1 > hi {
		t.Errorf("jitteredBackoff(1) = %v, want in [%v, %v]", d1, lo, hi)
	}

	// failures altos: siempre en el techo ± jitter, nunca más.
	dCapped := jitteredBackoff(20, base, max)
	if hi := time.Duration(float64(max) * 1.2); dCapped > hi {
		t.Errorf("jitteredBackoff(20) = %v, want <= %v (techo + jitter)", dCapped, hi)
	}
	if dCapped <= 0 {
		t.Errorf("jitteredBackoff(20) = %v, want > 0", dCapped)
	}

	// Crecimiento: con base=5s, el rango de jitter de failures=1 ([4s,6s])
	// y el de failures=3 (backoff=20s, [16s,24s]) no se tocan — una sola
	// llamada alcanza, sin necesidad de promediar muchas corridas.
	d3 := jitteredBackoff(3, base, max)
	if d3 <= d1 {
		t.Errorf("jitteredBackoff(3) = %v, want > jitteredBackoff(1) = %v — el backoff no está creciendo", d3, d1)
	}
}

// TestJitteredBackoffDefensiveDefaults: base/max <= 0 (un Adapter armado a
// mano, sin pasar por New()) cae en los defaults (5s/5min), nunca en un
// delay cero o negativo — un delay cero sería el mismo martilleo que este
// freno existe para arreglar.
func TestJitteredBackoffDefensiveDefaults(t *testing.T) {
	d := jitteredBackoff(1, 0, 0)
	if d <= 0 {
		t.Fatalf("jitteredBackoff con base/max en 0 = %v, want > 0", d)
	}
	if lo, hi := 4*time.Second, 6*time.Second; d < lo || d > hi {
		t.Errorf("jitteredBackoff(1, 0, 0) = %v, want cerca del default 5s [%v, %v]", d, lo, hi)
	}
}

// TestScheduleReconnectIsFailOpen es el test que pidió Citrino (ajuste 2):
// con un connectFn que SIEMPRE falla, el loop tiene que seguir reintentando
// sin límite — nunca "se cansa". Delays minúsculos para que corra rápido;
// cada intento se confirma por canal en vez de por temporización, así que
// no es un test de timing.
func TestScheduleReconnectIsFailOpen(t *testing.T) {
	calls := make(chan struct{}, 64)
	a := &Adapter{
		reconnectBaseDelay: time.Millisecond,
		reconnectMaxDelay:  4 * time.Millisecond,
		connectFn: func() error {
			calls <- struct{}{}
			return errConnectAlwaysFails
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.scheduleReconnect(ctx)

	const wantAttempts = 25
	for i := 0; i < wantAttempts; i++ {
		select {
		case <-calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("dejó de reintentar después de %d intentos — el freno no puede rendirse nunca", i)
		}
	}
}

var errConnectAlwaysFails = errors.New("connect: red inalcanzable")

// TestScheduleReconnectNilConnectFnNoOp: un Adapter armado a mano para un
// test, sin connectFn, no puede reventar cuando algo dispara un
// *events.Disconnected — mismo convenio nil-safe que Store/Router/Bus.
func TestScheduleReconnectNilConnectFnNoOp(t *testing.T) {
	a := &Adapter{reconnectBaseDelay: time.Millisecond, reconnectMaxDelay: time.Millisecond}
	a.scheduleReconnect(context.Background())
	// Si esto reventara, sería en una goroutine en segundo plano — darle
	// tiempo a aparecer antes de que el test dé por bueno el resultado.
	time.Sleep(20 * time.Millisecond)
}

// TestScheduleReconnectStopsOnContextCancel: la única salida legítima del
// loop (ver el doc de scheduleReconnect) — cancelar ctx corta los
// reintentos en vez de dejarlos correr contra un proceso que ya se está
// apagando.
func TestScheduleReconnectStopsOnContextCancel(t *testing.T) {
	calls := make(chan struct{}, 64)
	a := &Adapter{
		reconnectBaseDelay: 30 * time.Millisecond,
		reconnectMaxDelay:  30 * time.Millisecond,
		connectFn: func() error {
			calls <- struct{}{}
			return errConnectAlwaysFails
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.scheduleReconnect(ctx)

	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("nunca llegó el primer intento")
	}
	cancel()

	// Drenar cualquier intento ya en vuelo, y confirmar que no aparece uno
	// nuevo bastante después del delay configurado.
	select {
	case <-calls:
	default:
	}
	select {
	case <-calls:
		t.Error("connectFn se siguió llamando después de cancelar ctx")
	case <-time.After(150 * time.Millisecond):
	}
}

// TestReconnectResetsOnlyAfterSustainedConnection es el corazón del
// contrato: whatsmeow resetea SU contador en el primer handshake exitoso,
// aunque la conexión dure segundos — este freno tiene que resetear el
// SUYO solo tras sobrevivir reconnectStableAfter, nunca antes.
func TestReconnectResetsOnlyAfterSustainedConnection(t *testing.T) {
	t.Run("flapping: NO resetea", func(t *testing.T) {
		a := &Adapter{
			reconnectBaseDelay:   time.Hour, // no queremos que dispare solo durante el test
			reconnectMaxDelay:    time.Hour,
			reconnectStableAfter: 50 * time.Millisecond,
			connectFn:            func() error { return nil },
		}
		a.handleEvent(&events.Disconnected{}) // fallo 1
		a.clearErrorState()
		a.armReconnectStableTimer() // conecta, arranca la ventana de estabilidad

		time.Sleep(10 * time.Millisecond) // bastante MENOS que reconnectStableAfter
		a.handleEvent(&events.Disconnected{})

		a.reconnectMu.Lock()
		got := a.reconnectFailures
		a.reconnectMu.Unlock()
		if got != 2 {
			t.Errorf("reconnectFailures = %d, want 2 (la caída antes de sostenerse NO debe resetear)", got)
		}
	})

	t.Run("sostenida: sí resetea", func(t *testing.T) {
		a := &Adapter{
			reconnectBaseDelay:   time.Hour,
			reconnectMaxDelay:    time.Hour,
			reconnectStableAfter: 15 * time.Millisecond,
			connectFn:            func() error { return nil },
		}
		a.handleEvent(&events.Disconnected{}) // fallo 1
		a.clearErrorState()
		a.armReconnectStableTimer()

		time.Sleep(60 * time.Millisecond) // bastante MÁS que reconnectStableAfter

		a.reconnectMu.Lock()
		got := a.reconnectFailures
		a.reconnectMu.Unlock()
		if got != 0 {
			t.Errorf("reconnectFailures = %d, want 0 (la conexión se sostuvo, tiene que resetear)", got)
		}
	})
}

// TestConnectOrScheduleRetryAlwaysReturnsNilAndSchedulesOnFailure es el
// test que pidió Citrino (ajuste 3): un fallo del PRIMER connect (la
// máquina arranca antes de que la red levante) no puede volver como error
// — antes de T99, ese error llegaba tal cual a corepipeline.Controller.
// Start, que NUNCA levanta el pipeline/MCP/REST si gw.Start falla, y
// main.go lo trata con log.Fatalf: el proceso entero moría por una red que
// todavía no había levantado. Acá se prueba: (a) siempre nil, (b) el fallo
// queda agendado en el mismo freno que cualquier caída posterior (se
// reintenta solo).
func TestConnectOrScheduleRetryAlwaysReturnsNilAndSchedulesOnFailure(t *testing.T) {
	calls := make(chan struct{}, 8)
	first := true
	a := &Adapter{
		reconnectBaseDelay: time.Millisecond,
		reconnectMaxDelay:  2 * time.Millisecond,
		connectFn: func() error {
			calls <- struct{}{}
			if first {
				first = false
				return errConnectAlwaysFails // "arranca antes de que la red levante"
			}
			return nil // el reintento en segundo plano encuentra la red ya arriba
		},
	}

	err := a.connectOrScheduleRetry(context.Background())
	if err != nil {
		t.Fatalf("connectOrScheduleRetry() = %v, want nil — un fallo de connect no puede tirar abajo Start()", err)
	}

	// El primer llamado (el de connectOrScheduleRetry mismo) ya consumió
	// un valor del canal; el segundo tiene que llegar solo, sin que nadie
	// más lo dispare, confirmando que quedó agendado.
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("no hubo un primer intento")
	}
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("el fallo inicial no quedó agendado para reintentar solo")
	}
}
