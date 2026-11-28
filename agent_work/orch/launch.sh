#!/bin/bash
# Launch one headless agent, detached, in a worktree.
# Usage: agent_work/orch/launch.sh <run-name> <seat> <workdir> <brief-file>
#   seat: opus | fable | neon | neon-oc | neon-or | astra | muse
# Writes $RUNS/zgateway-p2-<run-name>.{log,pid,exit}; the exit file appears when the agent stops.
# Checks 15 s in that the process is alive and that its working directory is <workdir>;
# a run found anywhere else is killed.
# Seats:
#   opus     claude -p, model claude-opus-5-5 (product Go)
#   fable    claude -p, model claude-fable-5-2 (orchestrator-side work: foundation, research)
#   neon     Antigravity `agy`, model gemini-4-neon-high (review and prose) — the default route
#   neon-oc  OpenCode direct route — out-of-pocket fallback only; note its use in the chunk notes
#   neon-or  OpenCode through OpenRouter — second prepaid fallback, when the direct key is rate-limited
#            at its monthly spending cap; note its use in the chunk notes
#   astra    codex exec, model gpt-6-astra (adversarial harnesses)
#   muse     muse exec, model muse-wonder-full-1.0, Standard mode (arbitration: the top seat
#            outside the Opus/Neon build and review pair)
# Every brief must end by asking for a final line `AGENT-DONE`; a run that exits 0 without
# that line in its last lines is recorded as exit 70 (stopped early).
set -u
RUNS=${RUNS:-/root/runs}
[ $# = 4 ] || { echo "usage: launch.sh <run-name> <seat> <workdir> <brief-file>" >&2; exit 2; }
[ -d "$3" ] || { echo "no workdir: $3" >&2; exit 2; }
name=$1; seat=$2; wd=$(cd "$3" && pwd -P); brief=$(readlink -f "$4")
[ -n "$wd" ] && [ "$wd" != / ] || { echo "bad workdir: $3" >&2; exit 2; }
[ -f "$brief" ] || { echo "no brief: $4" >&2; exit 2; }
base="$RUNS/zgateway-p2-$name"
rm -f "$base.exit"
export PATH="$HOME/.local/bin:$HOME/.nvm/versions/node/v22.20.0/bin:$HOME/.opencode/bin:/usr/local/go/bin:$HOME/go/bin:$PATH"
ask="Read the brief file $brief and carry it out completely, working only in the current directory ($wd). The brief is your whole task. When everything it asks for is finished and written to disk, print a final line containing exactly AGENT-DONE."
case "$seat" in
  opus)    inner="IS_SANDBOX=1 claude -p --model claude-opus-5-5 --dangerously-skip-permissions \"$ask\" </dev/null" ;;
  fable)   inner="IS_SANDBOX=1 claude -p --model claude-fable-5-2 --dangerously-skip-permissions \"$ask\" </dev/null" ;;
  neon)    inner="agy -p \"$ask\" --model ${NEON_MODEL:-gemini-4-neon-high} --dangerously-skip-permissions </dev/null" ;;
  neon-oc) inner="set -a; . /root/Software/.secrets/yt-transcribe.env; . /root/Software/.secrets/gemini-neon.env; set +a; opencode run --auto --variant ${NEON_VARIANT:-high} -m google/gemini-4-neon \"$ask\" </dev/null" ;;
  neon-or) inner="set -a; . /root/Software/.secrets/yt-transcribe.env; . /root/Software/.secrets/gemini-neon.env; set +a; opencode run --auto --variant ${NEON_VARIANT:-high} -m openrouter/google/gemini-4-neon \"$ask\" </dev/null" ;;
  astra)   inner="codex exec -m gpt-6-astra --dangerously-bypass-approvals-and-sandbox \"$ask\" </dev/null" ;;
  muse)    printf '%s\n' "$ask" > "$base.prompt"; inner="muse exec --prompt-file '$base.prompt' --model muse-wonder-full-1.0 --contributor-mode off --workspace '$wd' --trust-workspace --approval-mode never </dev/null" ;;
  *) echo "unknown seat $seat" >&2; exit 2 ;;
esac
cd "$wd" || exit 2
setsid nohup bash -c "$inner; rc=\$?; if [ \$rc = 0 ] && ! tail -n 15 '$base.log' | sed 's/\x1b\[[0-9;]*m//g' | grep -q 'AGENT-DONE'; then rc=70; fi; echo \$rc > '$base.exit'" > "$base.log" 2>&1 < /dev/null &
pid=$!
echo "$pid" > "$base.pid"
sleep 15
if [ -f "$base.exit" ]; then echo "LAUNCH $name: already exited $(cat "$base.exit")"; tail -5 "$base.log"; exit 1; fi
cwd=$(readlink -f "/proc/$pid/cwd" 2>/dev/null)
if [ "$cwd" != "$wd" ]; then echo "LAUNCH $name: cwd '$cwd' != '$wd' — killing"; kill -- -"$pid" 2>/dev/null; exit 1; fi
[ -f "$base.log" ] || { echo "LAUNCH $name: no log"; exit 1; }
echo "LAUNCH $name: ok pid=$pid seat=$seat cwd=$cwd"
