# c05 adversarial state

Goal: execute `/root/briefs/zgateway-p2-c05-adversarial.md`; findings in [RESULTS.md](RESULTS.md).

Status: complete; 32 attacks, 29 held and three broken. Failing attacks are intentionally retained.

Done: independent tests, default and 100,000-input runs, disposable PostgreSQL 17 differential checks, evidence and report. Build under attack: `7b156bdd603b6c8b85a250cd03d9ad3a10d3fbf3`. Harness changes are in the commit containing this file on `work/c05-adv`.

In progress: none. Next: orchestrator reviews the findings and routes product repairs or claim narrowing. No push or merge performed. No approval gate owned by this adversary.

Resume: use this worktree and the three commands in RESULTS.md. No detached runs remain. The tier R runner removes its container on exit. The root STATE.md was read but left untouched because the brief restricts every edit to this directory.

Environment note: the requested kb searches could not initialize: local search, `--global`, and `--root /root/Raw/knowledge` all failed with missing location/config messages before returning any search ID or hits. There was no search result to judge and no usable feedback ID. No knowledge-store files were opened or changed.
