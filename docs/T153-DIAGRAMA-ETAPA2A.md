# T153 etapa 2a — index.html al catálogo (ct-2026-09-08-1656)

Diagrama hecho a mano, no vía `dflux_resolve` — el codemap todavía no indexa
`internal/i18n`/`internal/dashboard/web` para Go/JS (mismo gap que etapa 1).

## La costura real — no es "archivo vs. archivo", es "quién es dueño del texto"

Citrino cortó la etapa 2 en "`index.html` estático" (2a) vs. "`app.js`
runtime" (2b). El mapeo real, hecho con un grep exhaustivo de cada
`.textContent =`/`.innerHTML =`/`.replaceChildren(`/`.placeholder =`/
`.title =` de `app.js` contra cada id de `index.html`, mostró que la línea
divisoria no es el archivo — es si `app.js` vuelve a escribir ese elemento:

```mermaid
flowchart TD
    A["texto visible en index.html"] --> B{"¿algún id de este\nelemento aparece del lado\nizquierdo de .textContent=/\n.innerHTML=/.replaceChildren(\nen app.js?"}
    B -->|"sí"| C["2b — es una CARCASA\nel valor por defecto del HTML\nes cosmético, se pisa en el\nprimer loadStatus() o antes"]
    B -->|"no"| D{"¿el texto vive en un\natributo (placeholder/\ntitle/aria-label/alt)?"}
    D -->|"sí"| E["2a — data-i18n-placeholder\n/title/aria-label/alt\n(applyI18n() extendido,\nluz verde de Citrino)"]
    D -->|"no, es textContent"| F{"¿el elemento tiene\nOTRO hijo real además\ndel texto a traducir?"}
    F -->|"sí (contenido mixto)"| G["envolver SOLO el texto suelto\nen un <span> nuevo — nunca\ndata-i18n en el padre\n(pisaría al hijo con textContent)"]
    F -->|"no, hijo único/ninguno"| H["data-i18n directo\nen el elemento existente"]
```

Ejemplos de C (2b, NO tocado en esta pasada): `#name`/`#num` (hero, pisado
por `loadStatus` con el string en español EN app.js), `#moodlabel`,
`#status`, todos los badges de valor (`#badgewa`.../`#badgehistory`),
`#more`, `#draftcount`, `#edittitle`/`#draftEditTitle`/`#draftRejectTitle`,
`#agentdelete_text`/`#approver_text`, `#qrnote` (con trampa: su default en
HTML es texto real, pero `loadStatus` lo reescribe con "Escaneá..." o
"Conectando…" según el estado — parece estático y no lo es), el placeholder
de `#search` (`SEARCHABLE_TABS`, vive en `app.js`), y `#herostatus_text` +
`#herostatus_pencil` (el caso más extremo: `renderHeroStatusView` los
destruye con `el.replaceChildren()` y los reconstruye desde cero en cada
render, con `title`/`aria-label` hardcodeados en el JS).

Ejemplo de G (contenido mixto, wrap mínimo): la barra de estado
(`Antena`/`Cifrado`/`Historial`/`Privacidad`, cada uno bare-text junto a un
`<b>` dinámico o un ícono), las 5 filas de la pestaña Reglas (label +
`<span class="dimnote">` anidado), el mini-stats (`cola:`/`enviados:`/
`agentes:` junto a un `<b>` contador).

## El endpoint no cambió de forma, solo de contenido

```mermaid
flowchart LR
    A["GET /api/i18n"] --> B["effectiveLang(store)"]
    B --> C["i18n.Catalog(lang)\netapa 1: {} vacío\netapa 2a: ~118 claves"]
    C --> D["{lang, language, texts}"]
    D --> E["app.js: loadI18n()\n+ applyI18n() (extendido)"]
    E -->|"[data-i18n]"| F["el.textContent = texts[clave]"]
    E -->|"[data-i18n-placeholder/title/\naria-label/alt]"| G["el.setAttribute(attr, texts[clave])"]
```

Etapa 2b: sacar los ~40 puntos de texto que arma `app.js` (badges, hero,
títulos de modal dinámicos, estados vacíos, `SEARCHABLE_TABS`) a un
`t(clave)` propio, llamado donde ese texto se construye — no un sweep como
`applyI18n()`, porque el valor se arma junto con datos (nombres, contadores)
que sí cambian por chat/estado.
