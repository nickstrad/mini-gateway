# c03 adversarial handoff

Goal: carry out `/root/briefs/zgateway-p2-c03-adversarial.md` against build
`085c617713da53c7f96e5ecac5d524ac82869cfc` on `work/c03-adv`.

Status: harness implemented; default, long campaign, and asynchronous race runs recorded.
See RESULTS.md for verdicts, reproduction commands, and limitations.

Done: three independent named attacks per claim (36 attacks), plus the subprocess helper.
Product files and shared state/index files remain untouched. Changes live only in this directory.
The report and tests are committed together; use `git log -1 --format=%H -- .` for that commit.

In progress: none. Next: orchestrator evaluates the failures and assigns any repair/review.
Owner of product decisions: orchestrator/builder. No human decision needed to reproduce.

Resume: from this worktree, run the commands in RESULTS.md. No detached agent or background
campaign is needed. BROKEN tests deliberately remain failing; do not weaken them to turn a gate green.

Knowledge assessment: reusable project findings and the exact reproducers are in RESULTS.md
and the three test files. No separate knowledge or work-scripts files were created because
this brief restricts edits to this directory. No new VM/environment finding needs saving.
