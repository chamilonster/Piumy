# Reglas invisibles (ct-2026-07-31)

Boss verbatim: *"las reglas por defecto no se ven"*. Citrino lo verificó
antes de despachar: `app.js` nunca llamaba `/api/admin/default-rules` ni
`/api/admin/type-rules` — existían, funcionaban, afectaban cómo contesta el
agente, y nadie podía verlos desde el tablero. Un chat sin reglas propias
mostraba `"(sin reglas propias)"`, que miente por omisión: sugiere que ahí
no rige nada, cuando puede estar aplicando la regla de tipo o la general.

## El problema — dos niveles sin interfaz

```mermaid
flowchart TD
    A["EffectiveRules(jid)\nparticular -> tipo/origen -> general"] --> B["app.js"]
    B -->|"llama"| C["/api/admin/chat-rules\n/api/admin/rules-default-new-number\n/api/admin/rules-default-contact"]
    B -.->|"NUNCA llama"| D["/api/admin/type-rules\n/api/admin/default-rules"]
    D -.-> E["existen, funcionan,\nnadie los ve"]
```

## El fix — 4 niveles visibles, un solo cálculo de jerarquía

```mermaid
flowchart TD
    subgraph Backend
        F1["GET default-rules\nGET type-rules?chat_type=\n(faltaban, eran POST-only)"]
        F2["rulesSourceFor()\nread.go, NO exportada\nmisma rama que EffectiveRules"]
        F3["GET /api/chats\nchatOut.rules_source nuevo"]
    end
    subgraph Frontend
        G1["#origindefaults\n+2 filas: General, Grupos\n(orden: General→Grupos→Nuevos→Contactos)"]
        G2["leyenda fija: precedencia\nen una línea, no se deduce"]
        G3["buildRulesControl()\nc.rules ? texto propio\n: RULES_SOURCE_LABEL[c.rules_source]\n  || 'Sin reglas en ningún nivel'"]
    end
    F1 --> G1
    F2 --> F3
    F3 --> G3
```

## rulesSourceFor — por qué NO es solo "llamar EffectiveRules por fila"

```mermaid
flowchart TD
    A["handleChats"] --> B["lee 4 KV UNA vez\ngeneral / tipo-grupo / origen-nuevo / origen-contacto"]
    B --> C{"por cada chat en el loop\n(ya existente, sin loop nuevo)"}
    C --> D["rulesSourceFor(c, isGroup, ...)\nmismo branching que EffectiveRules\npero sin I/O propio"]
    D --> E["chatOut.rules_source"]
```

`EffectiveRules` por fila habría significado hasta `dashboardChatLimit`
round-trips extra a la DB por request — acá las 4 KV se leen una vez y el
branching es puro Go sobre datos ya en memoria.

## La trampa conocida — `rules_type_individual`

```mermaid
flowchart LR
    A["SetTypeRules('individual', ...)\nEXISTE, persiste el valor"] -.->|"nadie lee esto"| B["EffectiveRules\n(dejó de leerlo desde M5,\neje origen lo reemplazó)"]
    A --> C["GET type-rules?chat_type=individual\nsigue respondiendo — simétrico con POST"]
    C -.->|"dashboard NO le pone editor"| D["a propósito — exponerlo repetiría\nel mismo bug al revés"]
```

Decisión de Citrino: **no reconectarlo** (cambiar el orden de resolución
con reglas reales cargadas es riesgo puro) y **no exponerlo** en el
dashboard. Queda anotado como deuda — reconectar o sacar el setter, sin
resolver en este cambio.

## Criterio de listo

- Los 4 niveles (particular ya existía; General, Grupos, Mensajes nuevos,
  Contactos) se ven y se editan desde el tablero, en orden de jerarquía,
  con leyenda explícita.
- Un chat sin reglas propias dice qué regla lo rige y de qué nivel viene;
  si de verdad no hay ninguna en ningún nivel, lo dice distinto.
- `go build ./... && go vet ./... && go test ./...` verde — tests nuevos:
  `TestChatsEndpointRulesSource` (los 6 casos: particular, tipo:grupo,
  general-por-grupo-sin-tipo, origen:nuevo, origen:contacto, ninguno),
  `TestGetSetTypeRulesRoundTrip`, `TestGetTypeRulesRejectsInvalidChatType`,
  `TestGetSetDefaultRulesRoundTrip`.
- `set_chat_rules`/`set_type_rules`/`set_default_rules` siguen bloqueadas
  por MCP — sin cambios, las reglas se escriben desde el tablero, nunca
  por un agente.
- `docs/MANUAL.md` actualizado.
