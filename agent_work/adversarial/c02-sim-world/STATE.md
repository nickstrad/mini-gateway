Goal: carry out /root/briefs/zgateway-p2-c02-adversarial.md against build 66e52dc27d8823a12481d67a7d7391818d766625.

Status: complete — 34 named attacks; HELD 29, BROKEN 5. See RESULTS.md for reproduction commands, classifications, scope, and evidence. Three product defects; two over-broad claims. Broken tests remain failing.

Completed: all eleven claims attacked at least three times; default suite 3.979 seconds; 300-seed overlapping-fault campaign passed; three independent live Postgres 17.11 conversations passed; go vet passed. Code, logs, report and runner are in this directory and included in the enclosing adversarial commit.

In progress: none. No background run remains.

Next: builder/orchestrator evaluates the three defects and two claim-scope decisions using RESULTS.md. No product repairs or review dispatch belong to this brief.

Open decisions: claim 7 send-window wording and claim 11 import-scope wording, owner builder/orchestrator. No blocker to this adversarial deliverable.

Resume: worktree /root/Software/mini-gateway-work/c02-adv, branch work/c02-adv. Run the default command in RESULTS.md (expected exit 1); tier-r.sh owns and cleans up its own container. No detached process needs relaunching. Root STATE.md is outside this brief's allowed edit scope.
