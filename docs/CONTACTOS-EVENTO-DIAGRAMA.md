# No llegan contactos en una instalación nueva (ct-2026-07-31)

Boss: *"la lista de contactos no carga"*. Instalación real medida dos veces,
~20 min de diferencia, números idénticos — no era "todavía sincronizando",
no llegaba nunca. Diagnóstico completo (mensaje aparte a Citrino) encontró
UNA causa raíz para `is_contact=0` y `chats.name` vacío en 714/720 chats;
`history_state` resultó ruido ya conocido, sin relación (columna muerta
desde ct-2026-07-24-2004).

## La causa — reloj, no evento

```mermaid
flowchart TD
    A["handleConnected\n(una vez, al conectar)"] --> B["go syncContacts(ctx)"]
    C["syncLoop\nticker 6h"] --> B
    B --> D["client.Store.Contacts.GetAllContacts()"]
    D -.->|"casi siempre vacío todavía —\napp-state sync y HistorySync\ntardan 'minutos a horas'"| E["backfillContacts\nescribe ~0 nombres"]
    F["whatsmeow: app-state sync\n(async, el servidor lo empuja)"] -.->|"nadie escucha\ncuándo termina"| D
    G["whatsmeow: HistorySync\nchunk PUSH_NAME"] -.->|"descartado como\n'degenerado', sin acción"| D
```

Ningún código distinto en dev: `handleConnected` se dispara en CADA
reconexión, no solo el primer pareo. Una instancia vieja acumuló muchas
corridas de `syncContacts` a lo largo de su vida — alguna cayó después de
que los datos ya habían llegado, y como `TouchChat`/`SetContactName` son
upserts persistentes, quedó pegado para siempre. Una instalación de 20
minutos tuvo una sola oportunidad, temprana.

## El fix — reaccionar al evento

```mermaid
flowchart TD
    A["whatsmeow escribe en\nStore.Contacts (interno,\nantes de despachar el evento)"] --> B{"¿qué evento\nlo señala?"}
    B -->|"chunk PUSH_NAME\nde HistorySync"| C["handleHistorySync\n(history.go)"]
    B -->|"AppStateSyncComplete\nName=critical_unblock_low"| D["handleEvent\n(inbound.go)"]
    C --> E["scheduleContactsSync()"]
    D --> E
    E --> F["time.AfterFunc debounced\n10s default, coalesce ráfaga"]
    F --> G["syncContacts(ctx)\nUNA vez por ráfaga"]
    H["syncLoop ticker 6h"] -.->|"red de seguridad,\nsin bajar ni tocar"| G
```

`critical_unblock_low` verificado leyendo `appstate/keys.go` de la
librería vendored — el nombre obvio ("regular") es otra colección
(config local de chat, no contactos).

## Por qué el debounce, y por qué 10s

```mermaid
flowchart LR
    A["chunk 1"] -->|"~2s"| B["chunk 2"]
    B -->|"~2s"| C["chunk 3"]
    C -->|"~3s"| D["chunk 4\n(burst real medido en\nhistory.go: 4 en ~7s)"]
    D -.->|"10s de silencio"| E["syncContacts corre UNA vez"]
```

`syncContacts` recorre cada contacto con un delay anti-ban real —
correrlo una vez por chunk sería lento y sin beneficio. 10s: más que el
burst observado, corto comparado con las 6h viejas.

## Verificación sin cuenta de WhatsApp nueva

Condición explícita de Citrino: no vincular una cuenta nueva para probar
sin hablarlo antes, y el bug solo se reproduce en una instalación nueva
(la del boss ya pasó ese momento). Resuelto reproduciendo la carrera
offline:

```mermaid
flowchart TD
    A["newTestWmeowClient\n(media_test.go, extendido)"] --> B["Store.Contacts real,\ndevice sintético persistido\n(antes solo Store.LIDs)"]
    B --> C["TestScheduleContactsSyncDebouncesBurstAndPicksUpData"]
    C --> D["3 llamadas en ráfaga\n(~10ms aparte)"]
    D --> E["PutPushName DIRECTO\na mitad de ráfaga\n(mismo orden que producción)"]
    E --> F["ventana de debounce corta (30ms)\nespera fuera de la ventana"]
    F --> G["chats.name recogió\nel push name que llegó a mitad"]
```

Más 4 tests de los disparadores en aislamiento (`TestHandleHistorySync
PushNameChunkSchedulesContactsSync`, `...ConversationsChunkDoesNotSchedule...`,
`TestHandleEventAppStateSyncCompleteContactsSchedulesSync`,
`...OtherCollectionIgnored`).

## Criterio de listo

- `go build ./... && go vet ./... && go test ./...` verde.
- El tick de 6h queda intacto, sin bajar — red de seguridad, no mecanismo
  principal.
- Comentario en el descarte del chunk `PUSH_NAME` explica por qué el
  contenido se descarta Y por qué el chunk en sí es una señal.
- Nada pedido de nuevo a WhatsApp — los datos ya los mandó; el fix es
  mirarlos en el momento correcto.
- `docs/MANUAL.md` actualizado.
- Instalación real del boss: sin escritura, sin reinicio — verificado
  offline, pendiente que el fix se despliegue ahí para confirmación final
  en vivo (fuera de este cambio, coordinar con Citrino).
