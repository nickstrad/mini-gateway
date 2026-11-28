# c12 adversarial harness state

Goal/spec: /root/briefs/zgateway-p2-c12-adversarial.md.
Status: complete; 48 attacks executed, 43 held and five broken; see RESULTS.md.
Base: 401f883f9b51534cf9cc3a69891cb99ddbce8b3a; branch work/c12-adv.
Tests, evidence and report are the deliverables in this directory. No background
runs remain. The commit containing this file records completion (git log -1).
Next: orchestrator reviews the five failures; owner resolves over-broad claims.
Resume: current worktree /root/Software/mini-gateway-work/c12-adv; run the default
command in RESULTS.md, or ADV_LONG=1 for both extended checks. Expect exit 1 while
the reported claims remain broken. Do not repair product code in this worktree.
