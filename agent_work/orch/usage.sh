#!/bin/bash
# Usage gauges, checked before each wave (PLAN.md section 12.1).
# Usage: agent_work/orch/usage.sh
# Prints what can be read without a terminal:
#   Neon (Antigravity): `agy -p "/usage"` — five-hour and weekly percentages remaining.
#   Claude: `claude -p "/usage"` prints the weekly percentages used (all models, and Fable).
#   Codex: no headless gauge; the newest run logs are searched for limit messages instead,
#   and an auth or limit failure of a run is the signal.
# Exit 0 always; the orchestrator reads the output.
export PATH="$HOME/.local/bin:$PATH"
echo "== Neon (agy /usage) =="
timeout 120 agy -p "/usage" 2>&1 | tail -6
echo "== Claude (claude -p /usage) =="
timeout 90 env IS_SANDBOX=1 claude -p "/usage" </dev/null 2>&1 | grep -iE "week|session|resets|limit" | head -6
echo "== limit or sign-in messages in the newest run logs =="
ls -t "${RUNS:-/root/runs}"/zgateway-p2-*.log 2>/dev/null | head -12 | while read -r f; do
  m=$(tail -n 30 "$f" | grep -iE 'usage limit|rate limit|quota|limit reached|not logged in|authentication|please run /login|401|expired' | tail -2)
  [ -n "$m" ] && echo "$f: $m"
done
echo "== done =="
