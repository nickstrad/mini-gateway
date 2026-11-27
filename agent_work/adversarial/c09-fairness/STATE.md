Goal: complete /root/briefs/zgateway-p2-c09-adversarial.md against the 15 builder claims.
Scope: only this directory; branch work/c09-adv, product base 27970a5.
Status: COMPLETE — 49 checks executed; HELD 37 / BROKEN 12. Failures intentionally preserved.
Done: 952134f initial checks; 431b433 decoder, mutation, PostgreSQL and replay campaigns; final results/evidence and harness corrections in the concluding commit (git log gives its SHA).
Results: RESULTS.md; machine-readable completed-check outputs: evidence.json.
Validation: final default run under three minutes; every ADV_LONG check executed, including real PostgreSQL, byte-identical CSV, fresh processes, 32-seed replay, and invariant-removal mutation. gofmt clean; go vet checked.
Next: orchestrator review of original wording versus NOTES.md qualifications. No product fix, merge or push authorized here.
Open decisions: orchestrator owns accepting the twelve over-broad-claim findings or revising the claims. No task blocker remains.
Resume: current worktree /root/Software/mini-gateway-work/c09-adv; no background test runs remain. Commands and exact seeds in RESULTS.md. Re-running the suite is expected to exit nonzero.
Knowledge assessment: durable project findings retained in RESULTS.md; no VM finding to save. kb local/global lookups failed before assigning search IDs, so no search-feedback ID exists.
