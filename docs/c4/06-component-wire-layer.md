# C4 Level 3: Components: wire layer

**Target architecture (planned). Nothing in this diagram is built yet.** Model: diagram `component-wire` in [model.json](model.json). Source of this page: [gen.py](gen.py).

pgproto3 gives message structs and nothing else. The framer, the session machines, SCRAM, transaction tracking, classification and error mapping are ours. Everything here is a pure state machine that accepts bytes in any fragments.

```mermaid
flowchart TB
  N_ENGINE["Engine"]
  N_WORK["Work vocabulary"]
  PGPROTO3[["pgproto3 (from pgx/v5)"]]
  subgraph SCOPE["Wire layer"]
    W_FRAMER["Framer"]
    W_CODEC["Codec boundary"]
    W_CLIENT["Client session machine"]
    W_BACKEND["Database session machine"]
    W_CYCLE["Query-cycle tracker"]
    W_SCRAM["SCRAM-SHA-256 functions"]
    W_LEXER["SQL lexer"]
    W_CLASSIFIER["Statement classifier"]
    W_ERRORS["Error contract and reply builders"]
  end
  N_ENGINE -->|1| W_CLIENT
  N_ENGINE -->|2| W_BACKEND
  N_ENGINE -->|3| W_CYCLE
  N_ENGINE -->|4| W_CLASSIFIER
  N_ENGINE -->|5| W_ERRORS
  W_CLIENT -->|6| W_CODEC
  W_BACKEND -->|7| W_CODEC
  W_CYCLE -->|8| W_CODEC
  W_ERRORS -->|9| W_CODEC
  W_CODEC -->|10| W_FRAMER
  W_CODEC -->|11| PGPROTO3
  W_CLIENT -->|12| W_SCRAM
  W_BACKEND -->|13| W_SCRAM
  W_CLASSIFIER -->|14| W_LEXER
  W_CLASSIFIER -->|15| N_WORK
  W_CYCLE -->|16| N_WORK
```

## Elements

| Element | Kind | What it is |
| --- | --- | --- |
| Framer | component | Ours. Cuts a byte stream into messages (type byte, 4-byte length, body) whatever the fragmentation, down to one byte at a time; enforces a maximum message size. |
| Codec boundary | component | The only product code that touches pgproto3: decodes a framed body into a message struct and encodes structs to bytes. pgproto3's own blocking reader is not used. |
| Client session machine | component | Client-facing. Answers TLS and GSS requests with no, reads the startup message, finds the tenant by user, runs SCRAM-SHA-256 (server side) or trust, sends the parameter set and proxy-issued key data, recognises cancel requests, takes in one query at a time, handles terminate. |
| Database session machine | component | Database-facing. Startup, SCRAM-SHA-256 (client side), collects server parameters and key data, reaches ready. |
| Query-cycle tracker | component | Follows a reply to its ReadyForQuery, reports the transaction status byte (idle, in transaction, failed), flags an unsolicited ParameterStatus (the connection is now dirty) and any protocol surprise. |
| SCRAM-SHA-256 functions | component | Pure functions on the standard library. Shared with the simulator's fake Postgres and test clients. |
| SQL lexer | component | Tokens only: strings, escape strings, dollar quotes, comments, identifiers; splits a message into statements. Not a parser. |
| Statement classifier | component | Turns a statement into a work unit: rejected (session-level feature), transaction control, provably read-only, shareable with its key, batchable. Conservative: what it cannot tokenise is never shared or batched. |
| Error contract and reply builders | component | One table: each refusal and failure, its SQLSTATE, whether anything reached the database, whether a write may have applied. Builds the ErrorResponse and the correct end marker. docs/error-contract.md is generated from it. |
| Engine | neighbouring container | internal/proxy. One single-threaded, sans-I/O state machine per instance: events in, actions out. Holds the client and database connection tables and the request pipeline with its stage seams. The simulator and the shell drive this same code. |
| Work vocabulary | neighbouring container | internal/work. Small plain types the cores are written against: tenant, unit of work and its class, share key, batchable flag, outcome. Carries no Postgres type. This is the extension seam for a second protocol. |
| pgproto3 (from pgx/v5) | softwareSystem, external | Go package github.com/jackc/pgx/v5/pgproto3: Postgres wire message structs with Encode and Decode. The one external dependency, used for message encoding only. |

## Relationships

| # | From | To | What | How |
| --- | --- | --- | --- | --- |
| 1 | Engine | Client session machine | Client bytes in; events and bytes out | Go |
| 2 | Engine | Database session machine | Database bytes in; events and bytes out | Go |
| 3 | Engine | Query-cycle tracker | Reply bytes in; status and message boundaries out | Go |
| 4 | Engine | Statement classifier | Statement text in; work unit out | Go |
| 5 | Engine | Error contract and reply builders | Contract row in; reply bytes out | Go |
| 6 | Client session machine | Codec boundary | Decode and encode messages | Go |
| 7 | Database session machine | Codec boundary | Decode and encode messages | Go |
| 8 | Query-cycle tracker | Codec boundary | Decode message headers and status | Go |
| 9 | Error contract and reply builders | Codec boundary | Encode ErrorResponse and ReadyForQuery | Go |
| 10 | Codec boundary | Framer | Framed message bodies | Go |
| 11 | Codec boundary | pgproto3 (from pgx/v5) | Message structs: Encode, Decode | Go import |
| 12 | Client session machine | SCRAM-SHA-256 functions | Server side of the exchange | Go |
| 13 | Database session machine | SCRAM-SHA-256 functions | Client side of the exchange | Go |
| 14 | Statement classifier | SQL lexer | Tokens and statement boundaries | Go |
| 15 | Statement classifier | Work vocabulary | Produces work units | Go types |
| 16 | Query-cycle tracker | Work vocabulary | Produces outcomes | Go types |
