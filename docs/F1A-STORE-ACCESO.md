# F1a — Capa de acceso de store

Diagrama del flujo de datos de la capa de acceso (CRUD) sobre el esquema ya
migrado en F0. Cherry-pick limpio de Piumy `store.go` (~1292 líneas), sin
tocar `capi`/`capipush`/`mcpserver`/`restapi` (fuera de scope, F4).

```mermaid
flowchart TD
    subgraph Chat["chat.go"]
        TOUCH["TouchChat"]:::add
        SETMODE["SetMode\n(normaliza 'advanced'->'dedicated')"]:::add
        GETCHAT["GetChat / ListChats"]:::add
        SETMEM["SetChatMemory / SetChatContext"]:::add
        SETRULES["SetChatRules / SetIsBoss (privilegiado)"]:::add
        CLAIM["ClaimChat / ReleaseChat"]:::add
        EFFRULES["EffectiveRules\n(particular -> por tipo -> default)"]:::add
    end

    subgraph Msg["message.go"]
        ADDMSG["AddMessage\n(dedup: INSERT OR IGNORE PK chat_jid,id)"]:::add
        RECEIPT["SetDelivered / SetRead"]:::add
    end

    subgraph Pending["pending.go"]
        PENDCHATS["PendingChats\n(último msg inbound)"]:::add
        PENDDED["PendingDedicated\n(cola modo dedicated)"]:::add
        MARKH["MarkHandled"]:::add
    end

    subgraph Outbox["outbox.go"]
        ENQ["Enqueue / EnqueueWithModel"]:::add
        DUE["DueOutbox\n(retry_count/next_retry_ts backoff)"]:::add
        RETRY["SetOutboxRetry / DeadLetterOutbox"]:::add
        SENT["MarkSent"]:::add
    end

    subgraph Draft["draft.go"]
        ADDDRAFT["AddDraft / AddDraftWithConfirmer"]:::add
        APPROVE["ApproveDraft -> Enqueue"]:::add
        DISCARD["DiscardDraft"]:::add
    end

    DB[("SQLite\n7 tablas (F0)")]:::reuse

    TOUCH --> ADDMSG
    ADDMSG --> PENDCHATS
    ADDMSG --> PENDDED
    PENDDED --> MARKH
    APPROVE --> ENQ
    DUE --> RETRY
    Chat --> DB
    Msg --> DB
    Pending --> DB
    Outbox --> DB
    Draft --> DB

    classDef add fill:#cce5ff,stroke:#1565c0,color:#1b1b1b;
    classDef reuse fill:#d4f7d4,stroke:#2e7d32,color:#1b1b1b;
```

Sin dependencias a otros paquetes internos (store es agnóstico, como en
Piumy) — nada que flaguear.
