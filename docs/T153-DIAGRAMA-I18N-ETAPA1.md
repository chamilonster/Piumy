# T153 etapa 1 — el mecanismo de idioma, sin traducir nada (ct-2026-09-08-1656)

Diagrama hecho a mano, no vía `dflux_resolve` — el codemap todavía no indexa
`internal/i18n`/`internal/store`/`internal/restapi` para Go (gap ya conocido).

## Resolución del idioma efectivo — la elección manual siempre gana

```mermaid
flowchart TD
    A["GET /api/i18n"] --> B{"store.SettingLanguage\nvacío?"}
    B -->|"no (el dueño ya eligió)"| C["usa esa elección\n(es / en)"]
    B -->|"sí, nunca elegido"| D["i18n.Detect()\nlocale de la MÁQUINA que corre Piumy\n(no el del navegador)"]
    D --> E{"Windows?"}
    E -->|sí| F["registro HKCU\\Control Panel\\International\\LocaleName\n(detect_windows.go)"]
    E -->|no| G["env LC_ALL / LANG\n(detect_other.go — Linux/Mac, T113)"]
    F --> H{"prefijo reconocido\n(es-*, en-*)?"}
    G --> H
    H -->|"en-*"| I["EN"]
    H -->|"cualquier otra cosa"| J["ES (default)"]
    C --> K["i18n.Catalog(lang)\nfallback a ES si el idioma no existe"]
    I --> K
    J --> K
    K --> L["{lang, texts}\netapa 1: texts = {} (todavía nada extraído)"]
```

## Front-end — mecanismo cableado, sin traducir nada todavía

```mermaid
flowchart LR
    subgraph boot["bootstrap (una vez) + tras guardar Opciones"]
        A["loadI18n()\nGET /api/i18n"] --> B["state.i18n = {lang, texts}"]
        B --> C["applyI18n()\nreemplaza [data-i18n] con texts[clave]"]
    end
    C -.->|"etapa 1: ningún elemento\ntiene data-i18n todavía"| D["el tablero se ve\nEXACTAMENTE igual que hoy"]
    E["#configmodal → selector Idioma\n(Automático/Español/English)"] -->|"POST /api/admin/language"| F["store.SettingLanguage"]
    F --> A
```

Etapa 2 es sacar los textos de `index.html`/`app.js` al catálogo (poblar
`ES`/`EN` en `internal/i18n/lang.go`) y agregarles `data-i18n="clave"` a los
elementos — recién ahí `applyI18n()` empieza a traducir algo de verdad.
