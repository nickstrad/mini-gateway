# Adversarial harness state

Goal: execute `/root/briefs/zgateway-p2-c06a-adversarial.md` against builder tip
`cd3513b83d99c6dbd933b5e4295131c27c50728f`, with three independent attacks per claim.

Status: COMPLETE. 45 named attacks: 36 held, nine broken, with the broken tests retained.
RESULTS.md contains the attack table, exact reproduction commands, findings and limits.

Worktree: `/root/Software/mini-gateway-work/c06a-adv`, branch `work/c06a-adv`.
All authored changes are confined to this directory. Root STATE.md is deliberately
not edited: the task's explicit write boundary takes precedence over the global protocol.

Done: default run 26.764 s; extended run 103.975 s, including 10,000 seeds × 200
events and the promoted short tests. Both runs: 36 pass / nine fail, identical failing
test names. go vet passed; gofmt is clean. Artifacts are committed on this branch; the
commit at branch HEAD contains the harness and report. Builder base is pinned above.

Next: the coordinator evaluates the nine findings; no additional adversary work is pending.
No product repairs, push, merge, or additional reviewer dispatch is part of this brief.

Resume: inspect `long-run.jsonl` and `default-run.jsonl` (local logs, ignored by git).
The commands are ordinary foreground `go test` processes; use the terminal session's
exit status to distinguish completion. Re-run with PATH including `/usr/local/go/bin`.

Environment note: both `kb search` and `kb --global search` failed before allocating a
search ID because neither store location was configured. No hit was used, no corpus
was opened, and no feedback ID was available. No VM configuration was changed.

Knowledge assessment: the durable product findings and reproduction commands live in
RESULTS.md; no VM knowledge was learned that warrants a kb update. Reusable test
fixtures live with the harness, and write_report.py rebuilds the result table from the
JSON log. No separate docs/knowledge or work-scripts file was created because the brief
forbids writes outside this directory.
