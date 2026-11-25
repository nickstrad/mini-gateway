#!/usr/bin/env python3
"""Mark adversarial tests as settled by the orchestrator (a narrowed claim, or an attack whose
setup no longer applies after a fix). Inserts a t.Skip line as the first statement.

Usage (in the build worktree): adv-skip.py <chunk-dir> "<reason>" <TestName>...
The reason should name what was decided; the line also points at the chunk's ADVERSARIAL.md.
This is the only edit the orchestrator makes to an adversary's code.
"""
import glob, re, sys
chunk, reason, names = sys.argv[1], sys.argv[2], sys.argv[3:]
files = glob.glob("agent_work/adversarial/%s/*_test.go" % chunk)
for n in names:
    done = False
    for f in files:
        s = open(f).read()
        m = re.search(r"^func %s\(t \*testing\.T\) \{\n" % re.escape(n), s, flags=re.M)
        if m:
            line = '\tt.Skip(%s)\n' % ('"SETTLED by the orchestrator: ' + reason.replace('\\', '\\\\').replace('"', '\\"') + ' See agent_work/chunks/' + chunk + '/ADVERSARIAL.md."')
            open(f, "w").write(s[:m.end()] + line + s[m.end():])
            done = True
            break
    if not done:
        sys.exit("adv-skip: %s not found" % n)
print("skipped %d tests" % len(names))
