# c04 adversarial handoff

Goal: complete `/root/briefs/zgateway-p2-c04-adversarial.md`; builder claims are in `/root/briefs/zgateway-p2-c04-wire-sessions.md`.

Status: complete; 35 attacks, 28 held / 7 broken, failing regressions retained.

Base: `64facb5851f0ed6c92ced8bfcb798b8a70ee5d14`. Branch: `work/c04-adv`; worktree: `/root/Software/mini-gateway-work/c04-adv`.

Done: independent attack suite, live Postgres 17.11 oracles, default and long-campaign JSON logs, detailed RESULTS.md. Commit SHA is available from `git log -1 -- agent_work/adversarial/c04-wire-sessions`; no merge or push authorized.

In progress: nothing. No background campaign remains. The throwaway database was removed by the runner's exit trap.

Next: orchestrator assesses the five product/API-boundary failures and two over-broad claims in RESULTS.md; builder repairs or narrows the contract, then a fresh adversary reruns these unchanged attacks.

Open decisions: claim wording and accepted API preconditions, owned by the orchestrator/builder. No human gate requested by this task.

Resume: run the default command in RESULTS.md (expected exit 1 until repair); rerun tier-r.sh to refresh the live oracle or ADV_LONG=1 for the seeded campaign. Scripts and all task artifacts stay under this directory.
