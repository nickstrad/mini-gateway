# Phase 1 findings (2026-11-24)

Durable facts found while writing the Phase 1 proposal. Evidence paths are relative to the repository root.

## Postgres wire behaviour that the design depends on

Measured against PostgreSQL 17.11 with `agent_work/phase1/probes/pgwire/` (output beside it).

- One `Query` message can leave a transaction open: `BEGIN; SELECT 1` ends with status byte `T`. Whether a connection is free is known only from the reply's `ReadyForQuery`.
- Several `Query` messages written in one write are answered in order, each with its own `ReadyForQuery`; an error in one does not affect the next. But a query pipelined after a `BEGIN` runs inside that transaction. Pipelining different clients' statements is safe only when each is provably a single statement that is not transaction control.
- `SET` outlives the exchange that issued it. A change to a reported setting (for example `application_name`) makes the server send an unsolicited `ParameterStatus`, which can be used to detect a connection that is no longer clean. `DISCARD ALL` resets it.
- A backend terminated mid-query sends `FATAL 57P01` and then the stream ends with no `ReadyForQuery`.
- A read already running does not see a write that commits while it runs, even though the writer has been acknowledged. Joining a request to a read that is already running can therefore return data older than the request.
- Reply size is set by the data: one 55-byte query returned 200,000 rows, 20 MB.

## pgproto3

- `github.com/jackc/pgx/v5/pgproto3` (v5.13.0) compiles in with exactly two other packages, both internal to pgx (`internal/iobufpool`, `internal/pgio`). The rest of pgx's module graph appears in the module list but is not built.
- Message structs have `Decode(body)` and `Encode(dst)` and work without `pgproto3.Frontend`/`Backend` (the blocking readers). Framing — 1 type byte, 4 length bytes that include themselves, body — is about 15 lines of our own, and that is the part that must survive 1-byte reads.
- It provides no SCRAM exchange. A SCRAM-SHA-256 client is about 50 lines on the standard library (`crypto/pbkdf2` exists since Go 1.24) and is accepted by a real server.

## Object-store simulator

- The checkout is `/root/Software/object-store` (renamed from `decentralized-object-store` on 2026-11-23).
- Its network delivers datagrams and whole messages per node pair; it has no connection object, partial reads, back pressure or resets. See `agent_work/phase1/research/q8-object-store-simulator-evaluation.md` for what copies cleanly and what does not.
- Its determinism lint bans all floating point in checked packages. Cores here use integer arithmetic so the ban can stay on.

## c4-sdk 0.1.0

- The relationship kind `implementation` is accepted between code elements only. For packages and components, record "interface implementation" in the technology text instead.
- Elements with status `planned` are drawn dashed, and the legend must then contain an entry with a dashed stroke or decoding warns.
- `decodeArchitecture(json)` returns `{ ok, value: { bundle, index }, diagnostics }`; `buildScene` takes `value.index` and a diagram id.
- A container may appear in another container's component view only if the element is tagged `external`; `docs/c4/gen.py` generates a twin element (id prefix `n-`) for this.

## Building the site on vm-1

- The system Python on vm-1 has no `pip` and no `ensurepip`. To build the site locally, download the `Markdown` wheel from PyPI, unzip it into a scratch folder and set `PYTHONPATH` to it. CI installs it with `pip` as usual.
