# Chunk 4 — adversarial record (Astra, gpt-6-astra)

First pass, against the build tip: **HELD 28, BROKEN 7** —
`agent_work/adversarial/c04-wire-sessions/RESULTS.md`. (The Astra run was cut off by its
provider's content filter after the harness and the report were written; the orchestrator
committed its output unchanged.)

After the one repair round (orchestrator's run, 2026-11-25): **34 attacks pass, 0 fail,
1 settled by the orchestrator**.

Fixed product defects: invalid handshake-result values accepted; SCRAM messages not bounded
in the machine and on the wire; a handshake deadline classified under the wrong reason; the
fake's answer to a repeated TLS request (now `FATAL 0A000`, as Postgres 17); the unknown-user
challenge leaking through its iteration count.

## Narrowed claims (accepted by the orchestrator)

- **Claim 9** (`TestAdv_9_IndependentIssuerCollision`, skipped with a pointer here). As
  written: proxy-issued key data is unique per session. As narrowed: unique among the
  sessions of one issuer. Two issuers fed the same random stream issue the same keys. A
  proxy instance has one issuer, seeded from the system's random source in the shell
  (chunk 6); Postgres scopes cancel keys to one server the same way.
- **Claim 5.** As written: unknown user and wrong password are indistinguishable. As
  narrowed: indistinguishable when every stored verifier uses the same iteration count and
  salt length; the mock challenge for an unknown user uses the table's most common values.
  Follow-up for chunk 6: config validation warns (or fails) when the tenant table mixes
  iteration counts or salt lengths.

## Deviations from the brief forced by real Postgres 17 (accepted)

The GS2 header sent to a server that offers `SCRAM-SHA-256-PLUS` is `n,,`; protocol 3.x
(x > 0) is negotiated down rather than refused; the `postgres:17` image answers a GSS
request with `G` where the proxy and the fake answer `N` (the one listed fidelity difference).
