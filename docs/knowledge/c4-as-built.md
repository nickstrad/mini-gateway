# C4 set: marking what a chunk built (2026-11-24)

At a chunk landing, edit only the AS BUILT block at the top of `docs/c4/gen.py`, then regenerate.

## The edit

1. Compare each element description of the chunk's diagram with the code and the chunk's
   NOTES.md. Where they differ, fix the description or add the element first.
2. In the AS BUILT block:
   - `LANDED = N` — the chunk that just landed. The line under every title is made from it.
   - `BUILT["<element id>"] = N` for each element whose whole description is now true.
   - `PARTIAL["<element id>"] = "Built in chunk N: ... Not built: ..."` for an element of which
     only part exists. It stays `planned` (dashed) and the sentence is shown in its table row.
     Move an element from `PARTIAL` to `BUILT` when its last part lands.
   - `REL_BUILT` — leave empty unless one arrow needs an exception. A relationship is built
     when both its ends are; `"<rel id>": N` forces built, `"<rel id>": None` forces planned.
3. Do not list neighbour twins (`n-...`); they follow their container. People and external
   systems are listed like any element, and only when a built relationship needs them.

`gen.py` refuses an unknown id, a chunk above `LANDED`, a built component whose container is
in neither table, and a container in `BUILT` with a component that is not.

Statuses (c4-sdk 0.1.0 allows `existing`, `planned`, `mixed`): `gen.py` writes `existing` for
`BUILT`, `planned` for the rest, and `mixed` for a diagram holding both. `mixed` on an element
or relationship needs a `plannedNotes` entry or decoding fails; `gen.py` does not use it there.

## Commands (from the repository root)

```sh
python3 docs/c4/gen.py          # prints the built counts
# c4-sdk: a scratch folder OUTSIDE /tmp (see phase1-findings.md), script copied in
mkdir -p ~/c4-scratch && cd ~/c4-scratch && npm init -y && npm install c4-sdk@0.1.0
cp <repo>/agent_work/phase1/c4-check.mjs . && node c4-check.mjs <repo>/docs/c4/model.json
# want: decode ok: true | errors: 0 | warnings: 0 | scenes built: 12 of 12
cd <repo> && scripts/site.sh /tmp/c4-site
(cd /tmp/c4-site && python3 -m http.server 8765 --bind 127.0.0.1) &
agent_work/phase1/check-mermaid.py http://127.0.0.1:8765   # want: every line "ok", exit 0
kill %1; rm -rf /tmp/c4-site ~/c4-scratch
```
