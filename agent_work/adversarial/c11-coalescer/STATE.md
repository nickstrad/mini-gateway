# Adversarial checkpoint

Goal: fulfill `/root/briefs/zgateway-p2-c11-adversarial.md` against build `dcf0c48689ea68067a706819d948eea6851b21c6`.

Status: 57 named attacks implemented; default and ADV_LONG campaigns completed. Six literal-claim failures preserved. Final report and independent harness validation in progress.

Branch/worktree: `work/c11-adv`, `/root/Software/mini-gateway-work/c11-adv`.
All changes stay in this directory; root STATE.md is deliberately untouched under the brief's write restriction.

Next: inspect extra-run.log and evidence/*.exit, finish RESULTS.md, commit attacks and evidence. Do not fix product code, merge, or push.

Resume: default-run.jsonl and long-run.jsonl contain test results. The extra validation process is `validate-extra.sh`; if interrupted, inspect its logs and rerun once with diagnosis. Reproduction commands are in RESULTS.md when finalized.
