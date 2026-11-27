# CR-3 — adversarial record (Astra, gpt-6-astra)

Astra wrote a small harness (30 default cases and one opt-in campaign) against the tip of
the two builds, `65ddfff`: inputs near the listed shapes where real work might outgrow the
counted work, any change of behaviour, output or allocation by the counters, and verdicts
that differ between runs. **HELD 31, BROKEN 0.**

The review, not the harness, found what was wrong with the first two builds (whole paths
that moved no counter; counters that recorded distance instead of reads). After the repair
round (tip `1bf025b`) the harness was re-run by the orchestrator: 31 pass.

No claim was narrowed by an attack. The repair narrowed claim 6 itself: in the default
build string bodies and line comments are not counted; the whole-call claim holds under the
`clscount` build tag, which the gate's G3 stage and `scripts/ci-fast.sh` run
(`scripts/counted-growth.sh`). Review 2 judges it.
