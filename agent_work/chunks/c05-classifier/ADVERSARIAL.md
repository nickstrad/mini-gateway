# Chunk 5 — adversarial record (Astra, gpt-6-astra)

First pass, against the build tip: **HELD 29, BROKEN 3** —
`agent_work/adversarial/c05-classifier/RESULTS.md`.

After the one repair round (orchestrator's run, 2026-11-25): **28 attacks pass, 0 fail, 3 need the real-Postgres tier (they run under `MG_TIER_R=1` and passed in the adversary's own run),
1 settled by the orchestrator**.

Fixed product defects: time-dependent literals (`'now'::timestamp` and similar) were
classified coalescable; `71+-3` was lexed differently from `71+ -3` (Postgres splits a
trailing sign off an operator).

## Narrowed claim (accepted by the orchestrator)

**Claim 3** (`TestAdv_3_RejectBeforeLexError`, skipped with a pointer here). As written:
anything the lexer cannot fully tokenise is forwarded, never shared, never read-only. As
narrowed: it is never shared and never read-only; it is forwarded unless the part that
could be read already contains a reject-list feature (for example `DISCARD ALL; SELECT
'unfinished`), in which case the message is rejected. Two further cases reject something
because it cannot be read — a `set_config` whose setting name is not a plain string literal,
and an unreadable `search_path` value — because forwarding them could hide a session-level
change from every later check. All three are in NOTES.md and go into the chapter as stated
limits.
