#!/usr/bin/env python3
"""Update the three moving parts of STATE.md.

Usage: agent_work/orch/state.py [--status TEXT] [--progress TEXT] [--next TEXT] [--done TEXT]
  --status    replaces the line starting "Status:"
  --progress  replaces the line starting "In progress"
  --next      replaces the line starting "Next steps"
  --done      inserts a bullet at the top of the "Done:" list
The orchestrator runs it at every checkpoint, then commits STATE.md.
"""
import argparse, re
ap = argparse.ArgumentParser()
for k in ("status", "progress", "next", "done"):
    ap.add_argument("--" + k)
a = ap.parse_args()
s = open("STATE.md").read()
def line(prefix, text, s):
    out, n = re.subn(r"^%s.*$" % re.escape(prefix), lambda m: text, s, count=1, flags=re.M)
    if n != 1:
        raise SystemExit("state.py: no line starts with %r" % prefix)
    return out
if a.status: s = line("Status:", "Status: " + a.status, s)
if a.progress: s = line("In progress", "In progress: " + a.progress, s)
if a.next: s = line("Next steps", "Next steps, in order: " + a.next, s)
if a.done:
    if "\nDone:\n" not in s:
        raise SystemExit("state.py: no Done: list")
    s = s.replace("\nDone:\n", "\nDone:\n- " + a.done + "\n", 1)
open("STATE.md", "w").write(s)
