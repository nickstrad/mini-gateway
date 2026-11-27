# c08 admission adversarial state

Goal: finish `/root/briefs/zgateway-p2-c08-adversarial-2.md` against build
`78510fae022d2b96b53ffc0ffbe129572e98d5fe` on `work/c08-adv`.

Status: complete; 46 held, 6 broken. RESULTS.md contains all cases, evidence,
classification, limits, and exact replay commands. All three inherited Go test
files are unchanged. Both default and ADV_LONG=1 suites were rerun to completion;
default took 60.859 seconds. Raw JSON logs were removed after report generation.

Done: claims and builder semantics reviewed; results recorded. Product files and
root STATE.md untouched under the brief's directory restriction. This directory's
commit is the checkpoint; identify it with `git log -1 -- agent_work/adversarial/c08-admission`.

Next: owner reconciles the six retained failures (five underlying findings).
No background runs remain, and no approval or implementation changes are requested.

Resume: `/root/Software/mini-gateway-work/c08-adv`, branch `work/c08-adv`.
Run the commands in RESULTS.md; exit 1 is expected until findings are addressed.
