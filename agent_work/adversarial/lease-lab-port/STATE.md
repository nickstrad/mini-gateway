Goal: execute /root/briefs/zgateway-p2-lease-port-adversarial.md against build 9f0378f.
Status: default campaign running; inherited harness retained and checked.
Scope: writes only in this directory; root STATE.md is deliberately unchanged per the brief.
Plan: 36 attacks, three per C1–C12 (the minimum of three per claim takes precedence over the approximate 15–25 total). Long mutation and repetition checks require ADV_LONG=1.
Done: initial harness commit 447d043; default live session-reset probes pass.
Next: finish default run, execute long campaign, classify failures, write RESULTS.md and evidence.json, commit.
Environment: kb default and --global searches failed before returning a search ID; feedback cannot target them. No store initialized or changed.
Resume: branch work/lease-port-adv. Default output default.jsonl; subprocess deadlines apply. Temporary overlays are under .scratch, removed by TestMain.
