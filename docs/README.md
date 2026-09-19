# piumy-gateway — EMPEZÁ ACÁ

Core server en **Go**: el puente entre **whatsmeow** (cliente de WhatsApp embebido, librería Go en el mismo proceso) y el **agente** (por **cAPI + MCP**). Es la evolución de Piumy con el gateway de WhatsApp reimplementado limpio; el cliente se enchufa por contrato (interfaz `gateway.Gateway`).

> **Pivote (ct-2026-07-11, ST-E):** el plan original apuntaba a **open-wa** (cliente Node externo); se pivoteó a **whatsmeow** (puro-Go, un solo binario, sin Node, QR en el mismo proceso). `internal/openwa` fue **borrado**. Los docs de fase (F0–F5) son históricos y todavía mencionan open-wa; la tesis vigente está en `../../CLAUDE.md`.

Diseño **cerrado, listo para implementar**. El plan completo vive en estos docs; un leader (Citrino) y un programmer (Tourmaline) arrancan leyendo esto, sin más contexto.

## Roles

- **Citrino (leader / arquitecto):** sostiene el diseño, corta los subcontratos por fase, audita. No pica código pesado.
- **Tourmaline (programmer):** implementa bajo subcontrato, una fase por vez, con la disciplina del proyecto.

## Orden de lectura (obligatorio antes de tocar nada)

1. **`../../CLAUDE.md`** — la regla estricta de código limpio (anti-noodles). No negociable.
2. **`MIGRATION-PLAN.md`** — inventario de cherry-pick (qué se migra tal cual, qué se reescribe, qué se deja) + el plan por fases (F0→F5) + el diagrama objetivo + el contrato del adaptador open-wa.
3. **`AGENT-BEHAVIOR.md`** — "el core del core": cómo el agente usa cada atributo de la DB (rules/memory/context), el semáforo de riesgo, y dónde vive el gate duro.

## Fuente del cherry-pick

Piumy vive en **`C:\proyectos\Piumy\coderoot`** (módulo Go `pimywa`, código en `core/internal`). Se hace cherry-pick **de estructura, no de código pegado**: el esquema de la DB, los contratos de las tools MCP, el formato de config. La lógica se reimplementa limpia, **mirando el código de Piumy como referencia** para no perder edge-cases ya pagados (anti-ban del `governor`, retry/backoff del `outbox`, receipts). El inventario exacto (paquete → acción) está en `MIGRATION-PLAN.md`.

## Dónde se construye

Este es el **CodeRoot** del proyecto (`piumy-gateway/coderoot`, módulo Go `piumy-gateway`, repo git propio). El código va en `internal/`; los diagramas de cada sub-cambio, acá en `docs/`.

## Primer paso

**F0 (esqueleto) → F1 (cherry-pick de los paquetes limpios).** Estas dos fases NO dependen de ninguna decisión pendiente del boss — son bajo riesgo, arrancá por ahí. F2 (interfaz+pipeline) tampoco. Recién F3/F4 tocan los puntos a confirmar (ver abajo).

## Puntos a confirmar con el boss (antes de F3/F4)

Ya hay una **propuesta de referencia decidida** para cada uno en `AGENT-BEHAVIOR.md` — no son preguntas en blanco, son diseños que solo necesitan el OK del boss al llegar a esas fases:
1. Semáforo del header cAPI (`boss/caution/danger` + `rule_ref`).
2. memory vs context (permanente / del momento).
3. confirmation_mode (default por tipo, rules override).
4. media (post-MVP).

## Disciplina (de `CLAUDE.md`)

Por cada sub-cambio no-trivial: **diagrama de flujo (Mermaid) → validar con D'Flux (`dflux_resolve`) → codear → `ponytail-review` → `go build/vet/test` verde.** El diagrama va en `docs/`.
