# Chunk 5 (rework) — adversarial record: two passes

By Nick's ruling of 2026-11-26 (chunk 5 only) the adversarial pass was done twice, by two
seats that did not see each other's work, and reconciled in `RECONCILIATION.md`.

- **Astra** (gpt-6-astra), `agent_work/adversarial/c05-classifier/`: first pass **HELD 31,
  BROKEN 5**. (Its run was cut by its provider's content filter after the report was
  written; the orchestrator committed its output unchanged.)
- **Neon** (gemini-4-neon-high, a fresh agent separate from the reviewer),
  `agent_work/adversarial/c05-classifier-neon/`: first pass **HELD 33, BROKEN 5**.

One finding was common to both (quadratic work per setting name). Each seat found what the
other did not: Astra the lexer's quadratic operator runs and the row-level-security limit;
Neon the refused valid read, the reply counter and the parallel-plan re-runs.

After the one repair round (orchestrator's run, 2026-11-26): Astra's package **32 pass,
4 settled**; Neon's package **35 pass, 3 settled** (its four real-server attacks run with
`ADV_TIER_R=1`: 2 pass, 2 settled).

Fixed product defects: the lexer rescanned an operator run for each operator (R2); the
classifier searched the whole statement once per setting name (R1); `temp` and `temporary`
used as ordinary names were refused (R6); a refused reply build was counted as built (R7).

## Narrowed claims (accepted by the orchestrator)

- **Claim 2, tables with policies** (`TestAdv_2_PlainTablePolicyChangesSession`,
  `TestAdv_2_PlainTablePolicyChangesRepeatedRows`, Astra; skipped with a pointer here). As
  written: on plain tables no text that fits the safe shape changes session state or
  returns different rows on a re-run. As narrowed: plain tables without row-level security
  policies. A policy is code the query text does not show. For such databases `coalesce`
  is `off` or `hinted`. PLAN.md states the limit since `3881f72`.
- **Claim 2, what repeats** (`TestAdv_2_ParallelScanDifferentRowsOnRerun`,
  `TestAdv_2_FloatSumParallelNonAssociativity`, Neon; skipped). As written: the same rows
  on an immediate re-run. As narrowed: the same multiset of rows; row order without
  `ORDER BY`, aggregates that depend on input order and floating-point aggregates may
  differ between two valid runs under a parallel plan. Sharing hands each identical read
  in flight one valid result. The real-server oracle now has a 200,000-row table and
  checks that a parallel plan is really used.
- **Claim 6** (`TestAdv_6_UnreadableLocalArguments`, Astra; skipped). As written: an
  unmodelled construct is never refused. As narrowed: never refused for being
  untokenisable or unmodelled; a recognised reject-list feature whose argument cannot be
  read is refused (NOTES.md, "Narrowed claims").

## Attacks the repair made wrong (settled by the orchestrator)

The two passes disagreed with themselves or each other at two points; the reconciliation
decided each, and the losing test is skipped:

- `TestAdv_8_ErrorRowsRandomOrderAccounting` (Astra, held at first) counted a refused
  build of the pass-through row in `Total` and `ByRow`. Neon's
  `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts` called that a defect; R7 agreed.
- `TestAdv_1_ShareableContractWithRejectAndTx` (Neon, held at first) expected
  `SELECT local temp` and like reads to be refused. Neon's own
  `TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused` called that refusal a
  defect; R6 agreed.

## Courtesy gaps (not findings; claim 7)

A `UESCAPE` literal continued across a newline hides an advisory-lock call from the
reject list (Neon). Session safety is the reset's job (PLAN.md D8).

## Note for the gate

Astra's real-server attacks start their own Postgres container; under heavy load one
(`TestAdv_2_PlainTableCoercionAndAggregates`) once met "the database system is starting
up". It passes alone.
