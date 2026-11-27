Robustness tests of database proxy session reset
==============================================

Run from the repository root with Go on PATH:

```sh
PATH=/usr/local/go/bin:$PATH go test -count=1 ./agent_work/adversarial/lease-lab-port/...
PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -json -count=1 -timeout=45m ./agent_work/adversarial/lease-lab-port/... -run 'TestAdv_[4-8]_' > agent_work/adversarial/lease-lab-port/long.jsonl 2>&1
```

All probes log seed 271828; inputs are fixed, not generated from wall-clock time. Real Postgres tests require Docker and the postgres:17 image; record checks require DuckDB and the repository site dependencies. The existing tier-R harness builds the real binary and starts its own disposable database. Mutation probes use Go overlays confined to this directory and never edit product files. Run campaigns sequentially: TestMain owns and removes .scratch. Logs are retained in evidence/.

The default campaign runs the live probes together to amortize database startup. Mutation and 20-repeat checks are opt-in to bound default runtime. Three checks per each of the twelve numbered claims require 36 tests, exceeding the brief’s approximate size recommendation. The recorded long campaign selects the 15 deferred checks; together the two commands cover all 36.

C1–C2 use new combinations and immediate observations within a lease, not copies of builder cases. C3 feeds invalid observations directly to the positive-control counter. C4–C7 vary setup effects, reset shapes, or remove each new state kind through overlays. C8 repeats the claimed tests under shared host load. C9 independently calculates Git blob hashes. C10 relocates the report to a directory containing spaces and a quote. C11 checks exact reproduction and additionally probes whether invalid archived input fails closed; those last two are explicitly stronger than the literal historical reproduction claim. C12 checks the stated discovery roots and builds the site into an isolated directory.

Failures remain failing. See RESULTS.md for classification and evidence.json for machine-readable outcomes.
