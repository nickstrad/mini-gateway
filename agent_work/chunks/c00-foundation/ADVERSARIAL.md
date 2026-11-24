# Chunk 0 — adversarial record (Astra, gpt-6-astra)

First pass (source `4948b35`): **HELD 87, BROKEN 16** — `agent_work/adversarial/c00-foundation/RESULTS.md`.

After the one repair round (commits `6f25a5a`..`c0deb3b`): **HELD 102, BROKEN 1** when the
suite is re-run in a scratch copy (`attack.sh` rewrites its own report, so the committed
RESULTS.md still shows the first pass).

The one attack still reported BROKEN is `foundation-reuse`. Orchestrator's judgement: the
claim holds. The gate now refuses `GATE_STAGE=foundation` with exit 2 as soon as the tree
holds product Go; the attack expects exit 1 with G2 red and so does not recognise a refusal.
No result file is written by a refused run, so nothing can land from it.

Seven claims were narrowed in writing; the exact wording is in NOTES.md ("Narrowed claims"),
`docs/knowledge/foundation-gates.md` and `tools/detlint/README-tools.txt`.
