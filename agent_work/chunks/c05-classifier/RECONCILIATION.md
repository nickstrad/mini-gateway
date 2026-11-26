# Chunk 5 rework — the two adversarial passes and review 1, reconciled

Written by the orchestrator on 2026-11-26 under Nick's ruling of that day
(`/root/briefs/zgateway-c05-neon-adversarial-ruling.md`): the repair round covers
everything either adversarial pass found and the review's nits; an Astra finding that
Neon's pass did not reproduce is judged on its merits. Nothing is dropped.

Sources: Astra, `agent_work/adversarial/c05-classifier/RESULTS.md` (31 held, 5 broken);
Neon, `agent_work/adversarial/c05-classifier-neon/RESULTS.md` (33 held, 5 broken; briefed
without Astra's findings, in a tree that did not hold them); Neon review 1, `REVIEW-1.md`
(NITS, two minor).

| # | Finding | Astra | Neon adv. | Decision for the repair round |
|---|---|---|---|---|
| R1 | Claim 5: the classifier searches the whole statement once per `pg_settings`, `client_encoding` or `standard_conforming_strings` name — quadratic time | `TestAdv_5_WholeSpanRescanning` | `TestAdv_5_QuadraticSpanScanDoS` | Found by both. Product defect. Fix to linear work. |
| R2 | Claim 5: the lexer rescans the rest of an operator run for each operator it cuts — quadratic time | `TestAdv_5_OperatorSuffixRescanning` | not found | Astra only. **Judged real**: the orchestrator measured it on the unrepaired code — `SELECT ` and 64 KiB of `+` takes 37 s, 238 times the 4 KiB time. Product defect. Fix to linear work. |
| R3 | Claim 2: a row-level security policy on an ordinary table can change session state or the rows of a re-run | `TestAdv_2_PlainTablePolicyChangesSession`, `…ChangesRepeatedRows` | not found | Astra only. **Judged real, and a limit, not a code defect**: no reader of query text can see a policy. Claim 2 is narrowed to exclude tables with row-level security policies; the limit is stated (PLAN.md has it since `3881f72`); `coalesce` off or hinted is the setting for such databases. No code change. |
| R4 | Claim 2: on a table large enough for a parallel plan, a safe-shape query returns its rows in another order on a re-run; `string_agg` without `ORDER BY` and a `sum` over `double precision` return another value | not found | `TestAdv_2_ParallelScanDifferentRowsOnRerun`, `TestAdv_2_FloatSumParallelNonAssociativity` | Neon only. **Judged real, and an over-broad claim**: each run is a valid evaluation of the query, and sharing one valid result among identical reads in flight is correct. Claim 2 is narrowed: what repeats is the result as a multiset of rows for queries whose value SQL defines; row order without `ORDER BY`, aggregates that depend on input order, and floating-point aggregates may differ between two valid runs. The safe shape is not changed. The real-server oracle gains a table large enough for a parallel plan and compares as multisets. |
| R5 | Claim 6: `set_config` with a setting name or path value the classifier cannot read is refused | `TestAdv_6_UnreadableLocalArguments` | not found | Astra only. **Judged an over-broad claim**, as Astra says: the refusal is deliberate and already under Deviations in NOTES.md. Claim 6 is narrowed to that precise form. No code change. |
| R6 | Claim 6: `SELECT a FROM local temp` and three like it — valid reads where `local`, `global`, `replace`, `temp`, `temporary` are ordinary names — are refused as `temp-object`, while the same text is SafeRead | not found | `TestAdv_6_PlainTableQueriesWithUnreservedAliasesNotRefused` | Neon only. Product defect (a refusal outside the documented ones, and a contradictory result). Fix: the rule fires only where a temporary object can be made; every documented and corpus reject entry stays refused. |
| R7 | Claim 8: a `Build` call that is refused and writes nothing still counts in `Total` and `ByRow` | not found | `TestAdv_8_FailedBuildDoesNotInflateBuiltCounts` | Neon only. Product defect. Fix; correct the product test that pins the wrong count (`errors_test.go`, about line 275). |
| R8 | Review 1 finding 1: the ledger row for `TestClassifierArbProbe` overstates coverage (B5 missing, E1–E3 mislabelled, E4 only inside B6) | — | — | B5 and E4 are asserted since `5356992` (salvaged from the stopped repair). Correct the ledger row to say what is asserted. |
| R9 | Review 1 finding 2: dead helper `clsTierRReadOnly` | — | — | Delete it. |
| R10 | Neon's ledger audit: `TestReplyMetricsCoverEveryRow` exercises one row only | — | noted, not counted | Make the test do what its name says, or rename it and correct its ledger row. |
| R11 | Courtesy gap: a `UESCAPE` literal continued across a newline hides an advisory-lock call | — | listed as a courtesy gap | Not a finding (claim 7). List it under courtesy gaps in NOTES.md. No new reject rule in this chunk. |

Astra's and Neon's held attacks agree with each other; neither pass contradicts a held
result of the other.

The repair run stopped by the ruling is kept on the local branch
`parked/c05r-repair-stopped` (`5363eaf`). Its two tests
`TestLexOperatorRunsGrowLinearly` and `TestClassifierSpanKeywordChecksGrowLinearly`
(in `internal/pgwire/classify_test.go` there) are regression tests for R2 and R1 and are
taken up by the repair round. Its code is not: the round writes its own fix.
