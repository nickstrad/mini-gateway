# Adversarial chunk 0 checkpoint

Goal: complete `/root/briefs/zgateway-p2-c00-adversarial.md`.

Status: complete — 103 attacks in 307.4 seconds; HELD 87, BROKEN 16. Results, raw evidence and console transcript are written. All temporary copies are removed.

Artifacts: `attack.sh`, `RESULTS.md`, `evidence.json`. The script is the reusable reproduction index.

Resume: branch `work/c00-adv`, worktree `/root/Software/mini-gateway-work/c00-adv`. Run `./agent_work/adversarial/c00-foundation/attack.sh` to regenerate all evidence. No background child remains after normal completion; temporary clones are removed in a finally block.

Next: orchestrator evaluates BROKEN findings before landing; no fixes are authorized in this worktree. Root state and durable knowledge directories are outside the brief’s allowed write scope. Commit ID is available via git log for this directory.

Tested gate source: `4948b35415d89efbeeb672af728697f5a5e29afe`. Durable project findings are in RESULTS.md, and reusable reproduction logic is in attack.sh. No new VM knowledge was established. Both remain here because the brief prohibits writes to docs/knowledge and work-scripts.
