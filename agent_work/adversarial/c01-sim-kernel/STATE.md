# Adversarial chunk state

Goal/spec: `/root/briefs/zgateway-p2-c01-adversarial.md`; claims in the c01 builder brief. Findings and reproduction commands: [RESULTS.md](RESULTS.md).

Status: complete; 34 attacks, 30 held and four broken. Default and ADV_LONG=1 runs finished; raw evidence is in default.json and long.json. No background run remains.

Base: 19edca0f05e46a5762b9abf8833ee16fc06252c0. Branch/worktree: work/c01-adv, /root/Software/mini-gateway-work/c01-adv. The adversarial commit contains only this directory; identify it with `git log -1 --format=%H -- agent_work/adversarial/c01-sim-kernel`.

Next: coordinator routes four product defects to the builder and independent reviewer; rerun both commands in RESULTS.md after repairs. Broken tests intentionally remain failing. No decisions for Nick. Product defects are the builder's blockers, not incomplete adversarial work.

Resume: read RESULTS.md, inspect git status, and run the default suite with /usr/local/go/bin on PATH. ADV_LONG=1 runs the additional 2,048-seed campaign. Root STATE.md was not edited because the brief restricts every file edit to this directory.
