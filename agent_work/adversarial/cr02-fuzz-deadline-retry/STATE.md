# CR-2 adversarial handoff

Goal: execute `/root/briefs/zgateway-p2-cr02n-adversarial.md` against build `1a138cb943770bb8f72342d562d71291a1e7330c`.

Status: harness and report complete; 17 held / 4 broken. All broken assertions remain failing. See RESULTS.md for commands, interpretation, and exact coverage limits.

Done: 21 independent named attacks covering all six claims; default and ADV_LONG campaigns; Go vet. Artifacts are committed together on `work/cr02n-adv` (identify the commit with `git log -1`). Durable findings and reusable harness instructions are recorded in RESULTS.md within the only authorized output directory; no separate project-knowledge edits are permitted by this brief.

In progress: none after this handoff. No detached work needs resuming.

Next: builder/reviewer reconcile the two broad claims and two filesystem-snapshot findings. Owner: orchestration chain. No product repair, merge, or push is authorized here.

Resume: use `/root/Software/mini-gateway-work/cr02n-adv`, branch `work/cr02n-adv`; run the commands in RESULTS.md. Exit 1 is expected while these four claims remain broken. This local state file is used because the brief expressly forbids edits to root STATE.md and every path outside this directory.
