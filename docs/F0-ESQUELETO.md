# F0 — Esqueleto Go de piumy-gateway

Diagrama del sub-cambio F0: arranque mínimo (config → store → salida limpia),
sin lógica de negocio (eso es F1). El resto de `internal/*` queda como
andamiaje vacío para las fases siguientes.

```mermaid
flowchart TD
    MAIN["main.go"]:::add
    CFG["config.Load()\nenv PIUMY_*"]:::add
    STORE["store.Open(path)\nschema literal + migrate"]:::add
    DB[("SQLite\n7 tablas: chats/messages/outbox/\ndrafts/chat_groups/media/kv")]:::add
    LOG["log \"piumy-gateway up\""]:::add
    CLOSE["store.Close() (defer)"]:::add

    MAIN --> CFG
    CFG --> STORE
    STORE --> DB
    STORE --> LOG
    MAIN -.-> CLOSE

    classDef add fill:#cce5ff,stroke:#1565c0,color:#1b1b1b;
```

Andamiaje sin lógica (carpetas `internal/` del inventario, contenido llega en
F1/F2): `router governor eventbus state mcpguard capi capipush mcpserver
sessionbackup sysinfo netinfo dashboard autoreply bridge corepipeline openwa`.
