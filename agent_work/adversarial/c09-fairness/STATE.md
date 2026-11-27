Goal: independently check the 15 claims in /root/briefs/zgateway-p2-c09-fairness.md.
Scope: only this directory; branch work/c09-adv, product base 27970a5.
Status: harness written; initial executions underway; final evidence/report pending.
Done: 952134f initial public-API boundary and schedule checks. Added decoder overlays, invariant mutation, real-Postgres cases, cross-process replay.
Findings so far: original claims 3, 4 and 8 need qualifications already partly present in builder NOTES.md. Preserve failing assertions.
Next: finish runs; correct harness-only defects; run final default and ADV_LONG suite; write RESULTS.md and commit.
Resume: inspect local run.txt and long-run.jsonl plus processes whose command includes c09-fairness.test. No detached conductor; foreground test runs are owned by this session.
Environment: kb local and global lookups failed before assigning search IDs (no configured location); no search feedback ID exists.
