HELD: 87  BROKEN: 16

# Foundation gate adversarial results

Tested source `4948b35415d89efbeeb672af728697f5a5e29afe`. Runtime: 307.4 seconds. Full suite.

Run from the repository root: `./agent_work/adversarial/c00-foundation/attack.sh`. Each attack starts in a fresh disposable local Git clone under `/tmp/zgw-c00-adv/`. The seed has a passing full gate. Fixtures are committed before checks except intentional dirty-tree attacks. Evidence includes stdout, per-check logs and result JSON; see `evidence.json`. A BROKEN label means the stated claim did not hold, including documented limitations; it does not assert every partial-check bypass survives the full gate.

Landing experiments run only in disposable clones. A Git wrapper refuses merge, push and fetch before any side effect. `ATTACK-BLOCKED-merge` proves the real landing checks accepted the evidence; it is a failure of the claim, not an actual merge. No real repository landing command or remote push runs.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `missing-site` — Missing site executable | HELD |
| 1 | `missing-rules` — Missing imports rule file | HELD |
| 1 | `broken-tools` — Lint tools cannot compile | HELD |
| 1 | `dirty-tree` — Full green dirty tree cannot count | HELD |
| 1 | `dirty-extra` — Uncommitted extra.sh must not produce clean landing evidence | BROKEN |
| 2 | `marker-gone` — Skeleton without its marker | HELD |
| 2 | `marker-still-there` — Built script with marker retained | HELD |
| 2 | `built-fails` — Built stage exits nonzero | HELD |
| 2 | `silent-skip` — Exit 3 with marker but without required text | HELD |
| 2 | `resurrect-marker` — Restore a marker after the stage was built | BROKEN |
| 3 | `root-format` — Unformatted Go in root | HELD |
| 3 | `root-compile` — Compile error in root | HELD |
| 3 | `root-vet` — Type-correct printf vet error in root | HELD |
| 3 | `root-tidy` — Unused requirement in root | HELD |
| 3 | `nested-format` — Unformatted Go in nested | HELD |
| 3 | `nested-compile` — Compile error in nested | HELD |
| 3 | `nested-vet` — Type-correct printf vet error in nested | HELD |
| 3 | `nested-tidy` — Unused requirement in nested | HELD |
| 4 | `det-clock` — Determinism: clock | HELD |
| 4 | `det-goroutine` — Determinism: goroutine | HELD |
| 4 | `det-rand` — Determinism: rand | HELD |
| 4 | `det-map-order` — Determinism: map-order | HELD |
| 4 | `det-float` — Determinism: float | HELD |
| 4 | `import-pool` — Adapter import in pool | HELD |
| 4 | `import-admission` — Adapter import in admission | HELD |
| 4 | `import-coalesce` — Adapter import in coalesce | HELD |
| 4 | `import-retry` — Adapter import in retry | HELD |
| 4 | `import-work` — Adapter import in work | HELD |
| 4 | `import-proxy` — Adapter import in proxy | HELD |
| 4 | `import-dot` — Import escape: dot | HELD |
| 4 | `import-blank` — Import escape: blank | HELD |
| 4 | `import-tagged` — Import escape: tagged | HELD |
| 4 | `import-test` — Import escape: test | HELD |
| 4 | `import-cgo` — Import escape: cgo | HELD |
| 4 | `import-pgproto` — Import escape: pgproto | HELD |
| 4 | `import-vendor` — Import escape: vendor | HELD |
| 4 | `import-nested` — Import escape: nested | HELD |
| 4 | `import-skip-tools` — Import escape: skip-tools | HELD |
| 4 | `import-skip-agent` — Import escape: skip-agent | HELD |
| 4 | `import-skip-site` — Import escape: skip-site | HELD |
| 4 | `import-reachable-testdata` — Explicitly imported core testdata package imports pgwire | BROKEN |
| 4 | `gomod-replace` — go.mod replace directive | HELD |
| 4 | `gomod-tool` — go.mod tool directive | HELD |
| 4 | `gomod-require` — go.mod require directive | HELD |
| 4 | `bound-zero` — Shell channel/growth: zero | HELD |
| 4 | `bound-variable` — Shell channel/growth: variable | HELD |
| 4 | `bound-growth` — Shell channel/growth: growth | HELD |
| 4 | `bound-alias` — Shell channel/growth: alias | BROKEN |
| 4 | `bound-helper` — Shell channel/growth: helper | BROKEN |
| 5 | `comment-block` — Build comment: block | HELD |
| 5 | `comment-fake-directive` — Build comment: fake-directive | HELD |
| 5 | `comment-generated` — Build comment: generated | HELD |
| 5 | `comment-test` — Build comment: test | HELD |
| 5 | `comment-tagged` — Build comment: tagged | HELD |
| 5 | `comment-outside` — Build comment: outside | HELD |
| 5 | `comment-testdata` — Build comment: testdata | BROKEN |
| 5 | `prose-string` — Final prose pass changes string | HELD |
| 5 | `prose-reorder` — Final prose pass changes reorder | HELD |
| 5 | `prose-testdata` — Final prose pass changes testdata | BROKEN |
| 5 | `prose-gomod` — Final prose pass changes gomod | HELD |
| 5 | `prose-rename` — Final prose pass changes rename | HELD |
| 5 | `prose-script` — Final prose pass changes script | BROKEN |
| 5 | `foundation-other` — Later chunk requests foundation stage | HELD |
| 5 | `foundation-reuse` — Product code passes when a later builder reuses c00-foundation identity | BROKEN |
| 6 | `test-fail` — Test failure: fail | HELD |
| 6 | `test-panic` — Test failure: panic | HELD |
| 6 | `test-compile` — Test failure: compile | HELD |
| 6 | `test-testmain` — Test failure: testmain | HELD |
| 6 | `test-race` — Listed race package writes concurrently | HELD |
| 7 | `site-link` — Broken relative link | HELD |
| 7 | `site-html-link` — Broken relative link in single-quoted raw HTML | BROKEN |
| 7 | `site-live` — Live chapter file missing | HELD |
| 7 | `site-unlisted` — Chapter file outside manifest | HELD |
| 8 | `extra-fail` — extra.sh fail | HELD |
| 8 | `extra-noexec` — extra.sh noexec | HELD |
| 8 | `extra-interpreter` — extra.sh interpreter | HELD |
| 8 | `extra-dangling` — Broken extra.sh symlink must fail closed | BROKEN |
| 9 | `land-red` — Landing rejects red | HELD |
| 9 | `land-partial` — Landing rejects partial | HELD |
| 9 | `land-off-branch` — Landing rejects off-branch | HELD |
| 9 | `land-code-after` — Landing rejects code-after | HELD |
| 9 | `land-missing-review` — Landing rejects missing-review | HELD |
| 9 | `land-fail-review` — Landing rejects fail-review | HELD |
| 9 | `land-nits` — Landing rejects nits | HELD |
| 9 | `land-missing-adversarial` — Landing rejects missing-adversarial | HELD |
| 9 | `land-unrebased` — Landing rejects unrebased | HELD |
| 9 | `land-dirty-main` — Landing rejects dirty-main | HELD |
| 9 | `land-bypass` — Landing rejects bypass | BROKEN |
| 9 | `land-inconsistent-partial` — Landing rejects inconsistent-partial | BROKEN |
| 9 | `land-wrong-chunk` — Landing rejects wrong-chunk | BROKEN |
| 9 | `land-extra-after` — Landing rejects extra-after | BROKEN |
| 10 | `canary-wrong-lint` — Canary rejects substitute lint failure | HELD |
| 10 | `canary-wrong-test` — Canary rejects substitute test failure | HELD |
| 10 | `canary-wrong-compile` — Canary rejects substitute compile failure | HELD |
| 11 | `env-partial` — Environment partial cannot count for landing | HELD |
| 11 | `env-out` — Environment out cannot count for landing | HELD |
| 11 | `env-short` — Environment short cannot count for landing | HELD |
| 11 | `env-small-budget` — Environment small-budget cannot count for landing | HELD |
| 11 | `env-stage` — Invalid/self-referential stage refused | HELD |
| 11 | `env-only` — Invalid/self-referential only refused | HELD |
| 11 | `env-base` — Invalid/self-referential base refused | HELD |
| 11 | `env-preprose` — Invalid/self-referential preprose refused | HELD |
| 11 | `env-stale` — Refused run must not leave an earlier landing result looking current | BROKEN |

## BROKEN: dirty-extra

Claim 1: Checks fail closed, and only a full green run on a clean tree counts for landing.

Attack: Uncommitted extra.sh must not produce clean landing evidence.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh dirty-extra`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=d9b9fca099d6840582425460f8a0bba3a0ceac0e stage=build dirty_tree=false only=all\nG0  tools        pass               2s \nG0  gofmt        pass               0s \nG0  build        pass               0s \nG0  vet          pass               0s \nG0  tools-vet    pass               1s \nG0  tools-test   pass               3s \nG0  tidy         pass               0s \nG1  detlint      pass               0s det-packages.txt lists no package yet\nG1  importlint   pass               0s \nG1  boundlint    pass               0s \nG1  paniccheck   pass               0s \nG2  comments     pass               0s comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e\nG3  unit         pass               0s \nG3  race         pass               0s race-packages.txt lists no package yet\nG4  sweep        skip-not-built     0s not built yet (marker scripts/.not-built/sweep)\nG5  fuzz         skip-not-built     0s not built yet (marker scripts/.not-built/fuzz)\nG6  tier-r       skip-not-built     0s not built yet (marker scripts/.not-built/tier-r)\nG7  csv          skip-not-built     0s not built yet (marker scripts/.not-built/csv-assert)\nG8  gen-docs     skip-not-built     0s not built yet (marker scripts/.not-built/gen-docs)\nG9  site         pass               1s \nG10 extra        pass               0s \ngate: overall=pass counts_for_landing=true -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": false,
    "overall": "pass",
    "counts_for_landing": true,
    "checks": [
      {
        "gate": "G0",
        "check": "tools",
        "status": "pass",
        "seconds": 2,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "gofmt",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "build",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "vet",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tools-vet",
        "status": "pass",
        "seconds": 1,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tools-test",
        "status": "pass",
        "seconds": 3,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tidy",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "detlint",
        "status": "pass",
        "seconds": 0,
        "note": "det-packages.txt lists no package yet"
      },
      {
        "gate": "G1",
        "check": "importlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "boundlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "paniccheck",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G2",
        "check": "comments",
        "status": "pass",
        "seconds": 0,
        "note": "comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e"
      },
      {
        "gate": "G3",
        "check": "unit",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G3",
        "check": "race",
        "status": "pass",
        "seconds": 0,
        "note": "race-packages.txt lists no package yet"
      },
      {
        "gate": "G4",
        "check": "sweep",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/sweep)"
      },
      {
        "gate": "G5",
        "check": "fuzz",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/fuzz)"
      },
      {
        "gate": "G6",
        "check": "tier-r",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/tier-r)"
      },
      {
        "gate": "G7",
        "check": "csv",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/csv-assert)"
      },
      {
        "gate": "G8",
        "check": "gen-docs",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/gen-docs)"
      },
      {
        "gate": "G9",
        "check": "site",
        "status": "pass",
        "seconds": 1,
        "note": ""
      },
      {
        "gate": "G10",
        "check": "extra",
        "status": "pass",
        "seconds": 0,
        "note": ""
      }
    ]
  }
}
```

Smallest suggested fix: Exclude only generated result/log files from git status, not the executable extra.sh.

## BROKEN: resurrect-marker

Claim 2: An unbuilt stage needs both marker and not-built message; a previously built stage cannot silently become skippable again.

Attack: Restore a marker after the stage was built.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh resurrect-marker`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=adac74b05b73ba090399160e061a067595ba3e0e stage=build dirty_tree=false only=G4\nG4  sweep        skip-not-built     0s not built yet (marker scripts/.not-built/sweep)\ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G4",
        "check": "sweep",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/sweep)"
      }
    ]
  }
}
```

Smallest suggested fix: Reject marker reintroduction relative to the accepted baseline/history; the current pair alone cannot prove never-built.

## BROKEN: import-reachable-testdata

Claim 4: G1 enforces determinism, every import/requirement restriction, and bounded shell channels and growth.

Attack: Explicitly imported core testdata package imports pgwire.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh import-reachable-testdata`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=a610b4bee52b0d656a281cb93f8dbd58df3c6f51 stage=build dirty_tree=false only=all\nG0  tools        pass               2s \nG0  gofmt        pass               0s \nG0  build        pass               0s \nG0  vet          pass               0s \nG0  tools-vet    pass               0s \nG0  tools-test   pass               4s \nG0  tidy         pass               0s \nG1  detlint      pass               0s det-packages.txt lists no package yet\nG1  importlint   pass               0s \nG1  boundlint    pass               0s \nG1  paniccheck   pass               0s \nG2  comments     pass               0s comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e\nG3  unit         pass               0s \nG3  race         pass               0s race-packages.txt lists no package yet\nG4  sweep        skip-not-built     0s not built yet (marker scripts/.not-built/sweep)\nG5  fuzz         skip-not-built     0s not built yet (marker scripts/.not-built/fuzz)\nG6  tier-r       skip-not-built     0s not built yet (marker scripts/.not-built/tier-r)\nG7  csv          skip-not-built     0s not built yet (marker scripts/.not-built/csv-assert)\nG8  gen-docs     skip-not-built     0s not built yet (marker scripts/.not-built/gen-docs)\nG9  site         pass               1s \nG10 extra        skip-none          0s the chunk has no gate/extra.sh\ngate: overall=pass counts_for_landing=true -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": false,
    "overall": "pass",
    "counts_for_landing": true,
    "checks": [
      {
        "gate": "G0",
        "check": "tools",
        "status": "pass",
        "seconds": 2,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "gofmt",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "build",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "vet",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tools-vet",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tools-test",
        "status": "pass",
        "seconds": 4,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tidy",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "detlint",
        "status": "pass",
        "seconds": 0,
        "note": "det-packages.txt lists no package yet"
      },
      {
        "gate": "G1",
        "check": "importlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "boundlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "paniccheck",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G2",
        "check": "comments",
        "status": "pass",
        "seconds": 0,
        "note": "comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e"
      },
      {
        "gate": "G3",
        "check": "unit",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G3",
        "check": "race",
        "status": "pass",
        "seconds": 0,
        "note": "race-packages.txt lists no package yet"
      },
      {
        "gate": "G4",
        "check": "sweep",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/sweep)"
      },
      {
        "gate": "G5",
        "check": "fuzz",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/fuzz)"
      },
      {
        "gate": "G6",
        "check": "tier-r",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/tier-r)"
      },
      {
        "gate": "G7",
        "check": "csv",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/csv-assert)"
      },
      {
        "gate": "G8",
        "check": "gen-docs",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/gen-docs)"
      },
      {
        "gate": "G9",
        "check": "site",
        "status": "pass",
        "seconds": 1,
        "note": ""
      },
      {
        "gate": "G10",
        "check": "extra",
        "status": "skip-none",
        "seconds": 0,
        "note": "the chunk has no gate/extra.sh"
      }
    ]
  }
}
```

Smallest suggested fix: Do not skip explicitly imported testdata packages; walk the reachable import graph or reject product imports of skipped directories.

## BROKEN: bound-alias

Claim 4: G1 enforces determinism, every import/requirement restriction, and bounded shell channels and growth.

Attack: Shell channel/growth: alias.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh bound-alias`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=bc4223d1953aecfaf83250bdbcb6aa9280ee1ab7 stage=build dirty_tree=false only=G1\nG1  detlint      pass               0s det-packages.txt lists no package yet\nG1  importlint   pass               0s \nG1  boundlint    pass               0s \nG1  paniccheck   pass               0s \ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G1",
        "check": "detlint",
        "status": "pass",
        "seconds": 0,
        "note": "det-packages.txt lists no package yet"
      },
      {
        "gate": "G1",
        "check": "importlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "boundlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "paniccheck",
        "status": "pass",
        "seconds": 0,
        "note": ""
      }
    ]
  }
}
```

Smallest suggested fix: Track field aliases/called growth helpers, or explicitly prohibit these forms in shell loops; this is a documented lint limitation, not a type-check failure.

## BROKEN: bound-helper

Claim 4: G1 enforces determinism, every import/requirement restriction, and bounded shell channels and growth.

Attack: Shell channel/growth: helper.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh bound-helper`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=485c73c24b1aa678c82f172d125fcc6c174acff3 stage=build dirty_tree=false only=G1\nG1  detlint      pass               0s det-packages.txt lists no package yet\nG1  importlint   pass               0s \nG1  boundlint    pass               0s \nG1  paniccheck   pass               0s \ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G1",
        "check": "detlint",
        "status": "pass",
        "seconds": 0,
        "note": "det-packages.txt lists no package yet"
      },
      {
        "gate": "G1",
        "check": "importlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "boundlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "paniccheck",
        "status": "pass",
        "seconds": 0,
        "note": ""
      }
    ]
  }
}
```

Smallest suggested fix: Track field aliases/called growth helpers, or explicitly prohibit these forms in shell loops; this is a documented lint limitation, not a type-check failure.

## BROKEN: comment-testdata

Claim 5: Build stage rejects added product Go comments; final stage permits no behavior change; later chunks cannot bypass G2 with foundation stage.

Attack: Build comment: testdata.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh comment-testdata`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=89fa0b75378c18db160659869563496a75fe888f stage=build dirty_tree=false only=G2\nG2  comments     pass               0s comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e\ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G2",
        "check": "comments",
        "status": "pass",
        "seconds": 0,
        "note": "comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e"
      }
    ]
  }
}
```

Smallest suggested fix: Narrow exclusions to inert fixtures, or reject Go fixtures outside the stated product scope instead of silently skipping them.

## BROKEN: prose-testdata

Claim 5: Build stage rejects added product Go comments; final stage permits no behavior change; later chunks cannot bypass G2 with foundation stage.

Attack: Final prose pass changes testdata.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh prose-testdata`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=5fadf8f2d7886b78c1f55156f9c13e98e0b3585b stage=final dirty_tree=false only=G2\nG2  astdiff      pass               0s code compared with pre-prose commit d9b9fca099d6840582425460f8a0bba3a0ceac0e\ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "final",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G2",
        "check": "astdiff",
        "status": "pass",
        "seconds": 0,
        "note": "code compared with pre-prose commit d9b9fca099d6840582425460f8a0bba3a0ceac0e"
      }
    ]
  }
}
```

Smallest suggested fix: At final stage compare all behavior-bearing non-Go files byte-for-byte (including testdata and scripts); allow only an explicit prose-file set.

## BROKEN: prose-script

Claim 5: Build stage rejects added product Go comments; final stage permits no behavior change; later chunks cannot bypass G2 with foundation stage.

Attack: Final prose pass changes script.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh prose-script`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=ae9d2c91c01466bc1504376f83b838c9b5dfafb9 stage=final dirty_tree=false only=G2\nG2  astdiff      pass               0s code compared with pre-prose commit d9b9fca099d6840582425460f8a0bba3a0ceac0e\ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "final",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G2",
        "check": "astdiff",
        "status": "pass",
        "seconds": 0,
        "note": "code compared with pre-prose commit d9b9fca099d6840582425460f8a0bba3a0ceac0e"
      }
    ]
  }
}
```

Smallest suggested fix: At final stage compare all behavior-bearing non-Go files byte-for-byte (including testdata and scripts); allow only an explicit prose-file set.

## BROKEN: foundation-reuse

Claim 5: Build stage rejects added product Go comments; final stage permits no behavior change; later chunks cannot bypass G2 with foundation stage.

Attack: Product code passes when a later builder reuses c00-foundation identity.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh foundation-reuse`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=c00-foundation sha=870561161fc28b4ada6212dfd5e8c723bb576190 stage=foundation dirty_tree=false only=all\nG0  tools        pass               1s \nG0  gofmt        pass               1s \nG0  build        pass               0s \nG0  vet          pass               0s \nG0  tools-vet    pass               0s \nG0  tools-test   pass               3s \nG0  tidy         pass               0s \nG1  detlint      pass               0s det-packages.txt lists no package yet\nG1  importlint   pass               0s \nG1  boundlint    pass               0s \nG1  paniccheck   pass               0s \nG2  comments     skip-foundation    0s stage foundation: chunk 0 has no product Go\nG3  unit         pass               0s \nG3  race         pass               0s race-packages.txt lists no package yet\nG4  sweep        skip-not-built     0s not built yet (marker scripts/.not-built/sweep)\nG5  fuzz         skip-not-built     0s not built yet (marker scripts/.not-built/fuzz)\nG6  tier-r       skip-not-built     0s not built yet (marker scripts/.not-built/tier-r)\nG7  csv          skip-not-built     0s not built yet (marker scripts/.not-built/csv-assert)\nG8  gen-docs     skip-not-built     0s not built yet (marker scripts/.not-built/gen-docs)\nG9  site         pass               2s \nG10 extra        skip-none          0s the chunk has no gate/extra.sh\ngate: overall=pass counts_for_landing=true -> agent_work/chunks/c00-foundation/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "foundation",
    "partial": false,
    "overall": "pass",
    "counts_for_landing": true,
    "checks": [
      {
        "gate": "G0",
        "check": "tools",
        "status": "pass",
        "seconds": 1,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "gofmt",
        "status": "pass",
        "seconds": 1,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "build",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "vet",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tools-vet",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tools-test",
        "status": "pass",
        "seconds": 3,
        "note": ""
      },
      {
        "gate": "G0",
        "check": "tidy",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "detlint",
        "status": "pass",
        "seconds": 0,
        "note": "det-packages.txt lists no package yet"
      },
      {
        "gate": "G1",
        "check": "importlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "boundlint",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G1",
        "check": "paniccheck",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G2",
        "check": "comments",
        "status": "skip-foundation",
        "seconds": 0,
        "note": "stage foundation: chunk 0 has no product Go"
      },
      {
        "gate": "G3",
        "check": "unit",
        "status": "pass",
        "seconds": 0,
        "note": ""
      },
      {
        "gate": "G3",
        "check": "race",
        "status": "pass",
        "seconds": 0,
        "note": "race-packages.txt lists no package yet"
      },
      {
        "gate": "G4",
        "check": "sweep",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/sweep)"
      },
      {
        "gate": "G5",
        "check": "fuzz",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/fuzz)"
      },
      {
        "gate": "G6",
        "check": "tier-r",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/tier-r)"
      },
      {
        "gate": "G7",
        "check": "csv",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/csv-assert)"
      },
      {
        "gate": "G8",
        "check": "gen-docs",
        "status": "skip-not-built",
        "seconds": 0,
        "note": "not built yet (marker scripts/.not-built/gen-docs)"
      },
      {
        "gate": "G9",
        "check": "site",
        "status": "pass",
        "seconds": 2,
        "note": ""
      },
      {
        "gate": "G10",
        "check": "extra",
        "status": "skip-none",
        "seconds": 0,
        "note": "the chunk has no gate/extra.sh"
      }
    ]
  }
}
```

Smallest suggested fix: Enforce that foundation has no product Go and bind stage/chunk identity at landing; the caller-controlled directory name is insufficient.

## BROKEN: site-html-link

Claim 7: G9 rejects broken relative links, missing live chapters and unlisted chapter files.

Attack: Broken relative link in single-quoted raw HTML.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh site-html-link`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=474b4f64cbc78a6b6952417cb6fb367f7a167780 stage=build dirty_tree=false only=G9\nG9  site         pass               1s \ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G9",
        "check": "site",
        "status": "pass",
        "seconds": 1,
        "note": ""
      }
    ]
  }
}
```

Smallest suggested fix: Parse rendered HTML anchors instead of matching only double-quoted href attributes.

## BROKEN: extra-dangling

Claim 8: G10 rejects failing or non-executable chunk extras and fails closed if an extra cannot run.

Attack: Broken extra.sh symlink must fail closed.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh extra-dangling`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 0,
  "stdout": "gate: chunk=attack sha=3aa697f96f06d4e4f991154abf7e1f71ad030d7b stage=build dirty_tree=false only=G10\nG10 extra        skip-none          0s the chunk has no gate/extra.sh\ngate: overall=pass counts_for_landing=false -> agent_work/chunks/attack/gate/result.json\n",
  "result": {
    "dirty_tree": false,
    "stage": "build",
    "partial": true,
    "overall": "pass",
    "counts_for_landing": false,
    "checks": [
      {
        "gate": "G10",
        "check": "extra",
        "status": "skip-none",
        "seconds": 0,
        "note": "the chunk has no gate/extra.sh"
      }
    ]
  }
}
```

Smallest suggested fix: Test -L as well as -e; reject dangling symlinks instead of recording skip-none.

## BROKEN: land-bypass

Claim 9: Landing rejects red/partial/mismatched evidence, post-gate code changes, missing/failed review, open NITS, missing adversarial evidence, an unrebased branch or dirty main.

Attack: Landing rejects bypass.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh land-bypass`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 91,
  "stdout": "ATTACK-BLOCKED-merge\n"
}
```

Smallest suggested fix: Reject LAND_NO_REVIEW=1 unless chunk is c00-foundation.

## BROKEN: land-inconsistent-partial

Claim 9: Landing rejects red/partial/mismatched evidence, post-gate code changes, missing/failed review, open NITS, missing adversarial evidence, an unrebased branch or dirty main.

Attack: Landing rejects inconsistent-partial.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh land-inconsistent-partial`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 91,
  "stdout": "ATTACK-BLOCKED-merge\n"
}
```

Smallest suggested fix: Require partial=false and dirty_tree=false independently of counts_for_landing.

## BROKEN: land-wrong-chunk

Claim 9: Landing rejects red/partial/mismatched evidence, post-gate code changes, missing/failed review, open NITS, missing adversarial evidence, an unrebased branch or dirty main.

Attack: Landing rejects wrong-chunk.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh land-wrong-chunk`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 91,
  "stdout": "ATTACK-BLOCKED-merge\n"
}
```

Smallest suggested fix: Require result.chunk to equal the requested chunk and foundation stage only for chunk zero.

## BROKEN: land-extra-after

Claim 9: Landing rejects red/partial/mismatched evidence, post-gate code changes, missing/failed review, open NITS, missing adversarial evidence, an unrebased branch or dirty main.

Attack: Landing rejects extra-after.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh land-extra-after`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "rc": 91,
  "stdout": "ATTACK-BLOCKED-merge\n"
}
```

Smallest suggested fix: Remove executable inputs such as gate/extra.sh from the post-gate agent_work exemption.

## BROKEN: env-stale

Claim 11: Builder environment settings cannot leave result.json claiming more than the invocation established.

Attack: Refused run must not leave an earlier landing result looking current.

Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh env-stale`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.

Observed (full logs in `evidence.json`):
```json
{
  "first": {
    "rc": 0,
    "stdout": "gate: chunk=attack sha=d9b9fca099d6840582425460f8a0bba3a0ceac0e stage=build dirty_tree=false only=all\nG0  tools        pass               2s \nG0  gofmt        pass               0s \nG0  build        pass               0s \nG0  vet          pass               0s \nG0  tools-vet    pass               0s \nG0  tools-test   pass               4s \nG0  tidy         pass               0s \nG1  detlint      pass               0s det-packages.txt lists no package yet\nG1  importlint   pass               0s \nG1  boundlint    pass               0s \nG1  paniccheck   pass               0s \nG2  comments     pass               0s comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e\nG3  unit         pass               0s \nG3  race         pass               0s race-packages.txt lists no package yet\nG4  sweep        skip-not-built     0s not built yet (marker scripts/.not-built/sweep)\nG5  fuzz         skip-not-built     0s not built yet (marker scripts/.not-built/fuzz)\nG6  tier-r       skip-not-built     0s not built yet (marker scripts/.not-built/tier-r)\nG7  csv          skip-not-built     0s not built yet (marker scripts/.not-built/csv-assert)\nG8  gen-docs     skip-not-built     0s not built yet (marker scripts/.not-built/gen-docs)\nG9  site         pass               1s \nG10 extra        skip-none          0s the chunk has no gate/extra.sh\ngate: overall=pass counts_for_landing=true -> agent_work/chunks/attack/gate/result.json\n",
    "result": {
      "chunk": "attack",
      "sha": "d9b9fca099d6840582425460f8a0bba3a0ceac0e",
      "dirty_tree": false,
      "stage": "build",
      "partial": false,
      "only": "",
      "overall": "pass",
      "counts_for_landing": true,
      "go_version": "go version go1.26.8 linux/amd64",
      "duckdb_version": "v1.5.5 (Variegata) d8cdaa33fd",
      "started": "2026-11-24T23:21:20Z",
      "finished": "2026-11-24T23:21:27Z",
      "seconds": 7,
      "comment_baseline": "6a780ec6e5e6860d343e72a058001e6503f9c11e",
      "preprose_commit": "none",
      "short": false,
      "sweep_seeds": 200,
      "fuzz_seconds": 20,
      "skip_not_built": [
        "G4 sweep",
        "G5 fuzz",
        "G6 tier-r",
        "G7 csv",
        "G8 gen-docs"
      ],
      "checks": [
        {
          "gate": "G0",
          "check": "tools",
          "status": "pass",
          "seconds": 2,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "gofmt",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "build",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "vet",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "tools-vet",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "tools-test",
          "status": "pass",
          "seconds": 4,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "tidy",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G1",
          "check": "detlint",
          "status": "pass",
          "seconds": 0,
          "note": "det-packages.txt lists no package yet"
        },
        {
          "gate": "G1",
          "check": "importlint",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G1",
          "check": "boundlint",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G1",
          "check": "paniccheck",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G2",
          "check": "comments",
          "status": "pass",
          "seconds": 0,
          "note": "comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e"
        },
        {
          "gate": "G3",
          "check": "unit",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G3",
          "check": "race",
          "status": "pass",
          "seconds": 0,
          "note": "race-packages.txt lists no package yet"
        },
        {
          "gate": "G4",
          "check": "sweep",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/sweep)"
        },
        {
          "gate": "G5",
          "check": "fuzz",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/fuzz)"
        },
        {
          "gate": "G6",
          "check": "tier-r",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/tier-r)"
        },
        {
          "gate": "G7",
          "check": "csv",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/csv-assert)"
        },
        {
          "gate": "G8",
          "check": "gen-docs",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/gen-docs)"
        },
        {
          "gate": "G9",
          "check": "site",
          "status": "pass",
          "seconds": 1,
          "note": ""
        },
        {
          "gate": "G10",
          "check": "extra",
          "status": "skip-none",
          "seconds": 0,
          "note": "the chunk has no gate/extra.sh"
        }
      ]
    },
    "logs": {
      "G1-boundlint.log": "boundlint: checked 0 files in 0 packages\n",
      "G3-race.log": "NOTE: race-packages.txt lists no package yet\n",
      "G0-tools-vet.log": "",
      "G1-detlint.log": "NOTE: det-packages.txt lists no package yet\n",
      "G9-site.log": "built 18 pages into /tmp/gate.zYjyE3/site\n",
      "G3-unit.log": "?   \tgithub.com/nickstrad/mini-gateway/internal/probe\t[no test files]\n",
      "G0-tidy.log": "",
      "G0-vet.log": "",
      "G0-gofmt.log": "\n",
      "G5-fuzz.log": "fuzz.sh: not built yet\n",
      "G7-csv.log": "csv-assert.sh: not built yet\n",
      "G4-sweep.log": "sweep.sh: not built yet\n",
      "G0-build.log": "",
      "G1-importlint.log": "importlint: checked 1 files\n",
      "G0-tools.log": "astdiff\nboundlint\ndetlint\nimportcheck\nimportlint\nnocomments\npaniccheck\n",
      "G0-tools-test.log": "ok  \tdetlint\t1.865s\nok  \tdetlint/cmd/astdiff\t0.065s\nok  \tdetlint/cmd/boundlint\t0.026s\nok  \tdetlint/cmd/importcheck\t0.018s\nok  \tdetlint/cmd/importlint\t0.017s\nok  \tdetlint/cmd/nocomments\t0.012s\nok  \tdetlint/cmd/paniccheck\t0.011s\n?   \tdetlint/internal/report\t[no test files]\n",
      "G1-paniccheck.log": "",
      "G8-gen-docs.log": "gen-docs.sh: not built yet\n",
      "G6-tier-r.log": "tier-r.sh: not built yet\n",
      "G2-comments.log": "NOTE: comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e\n"
    }
  },
  "second": {
    "rc": 2,
    "stdout": "gate: GATE_STAGE must be build, final or foundation, got 'invalid'\n",
    "result": {
      "chunk": "attack",
      "sha": "d9b9fca099d6840582425460f8a0bba3a0ceac0e",
      "dirty_tree": false,
      "stage": "build",
      "partial": false,
      "only": "",
      "overall": "pass",
      "counts_for_landing": true,
      "go_version": "go version go1.26.8 linux/amd64",
      "duckdb_version": "v1.5.5 (Variegata) d8cdaa33fd",
      "started": "2026-11-24T23:21:20Z",
      "finished": "2026-11-24T23:21:27Z",
      "seconds": 7,
      "comment_baseline": "6a780ec6e5e6860d343e72a058001e6503f9c11e",
      "preprose_commit": "none",
      "short": false,
      "sweep_seeds": 200,
      "fuzz_seconds": 20,
      "skip_not_built": [
        "G4 sweep",
        "G5 fuzz",
        "G6 tier-r",
        "G7 csv",
        "G8 gen-docs"
      ],
      "checks": [
        {
          "gate": "G0",
          "check": "tools",
          "status": "pass",
          "seconds": 2,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "gofmt",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "build",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "vet",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "tools-vet",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "tools-test",
          "status": "pass",
          "seconds": 4,
          "note": ""
        },
        {
          "gate": "G0",
          "check": "tidy",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G1",
          "check": "detlint",
          "status": "pass",
          "seconds": 0,
          "note": "det-packages.txt lists no package yet"
        },
        {
          "gate": "G1",
          "check": "importlint",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G1",
          "check": "boundlint",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G1",
          "check": "paniccheck",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G2",
          "check": "comments",
          "status": "pass",
          "seconds": 0,
          "note": "comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e"
        },
        {
          "gate": "G3",
          "check": "unit",
          "status": "pass",
          "seconds": 0,
          "note": ""
        },
        {
          "gate": "G3",
          "check": "race",
          "status": "pass",
          "seconds": 0,
          "note": "race-packages.txt lists no package yet"
        },
        {
          "gate": "G4",
          "check": "sweep",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/sweep)"
        },
        {
          "gate": "G5",
          "check": "fuzz",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/fuzz)"
        },
        {
          "gate": "G6",
          "check": "tier-r",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/tier-r)"
        },
        {
          "gate": "G7",
          "check": "csv",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/csv-assert)"
        },
        {
          "gate": "G8",
          "check": "gen-docs",
          "status": "skip-not-built",
          "seconds": 0,
          "note": "not built yet (marker scripts/.not-built/gen-docs)"
        },
        {
          "gate": "G9",
          "check": "site",
          "status": "pass",
          "seconds": 1,
          "note": ""
        },
        {
          "gate": "G10",
          "check": "extra",
          "status": "skip-none",
          "seconds": 0,
          "note": "the chunk has no gate/extra.sh"
        }
      ]
    },
    "logs": {
      "G1-boundlint.log": "boundlint: checked 0 files in 0 packages\n",
      "G3-race.log": "NOTE: race-packages.txt lists no package yet\n",
      "G0-tools-vet.log": "",
      "G1-detlint.log": "NOTE: det-packages.txt lists no package yet\n",
      "G9-site.log": "built 18 pages into /tmp/gate.zYjyE3/site\n",
      "G3-unit.log": "?   \tgithub.com/nickstrad/mini-gateway/internal/probe\t[no test files]\n",
      "G0-tidy.log": "",
      "G0-vet.log": "",
      "G0-gofmt.log": "\n",
      "G5-fuzz.log": "fuzz.sh: not built yet\n",
      "G7-csv.log": "csv-assert.sh: not built yet\n",
      "G4-sweep.log": "sweep.sh: not built yet\n",
      "G0-build.log": "",
      "G1-importlint.log": "importlint: checked 1 files\n",
      "G0-tools.log": "astdiff\nboundlint\ndetlint\nimportcheck\nimportlint\nnocomments\npaniccheck\n",
      "G0-tools-test.log": "ok  \tdetlint\t1.865s\nok  \tdetlint/cmd/astdiff\t0.065s\nok  \tdetlint/cmd/boundlint\t0.026s\nok  \tdetlint/cmd/importcheck\t0.018s\nok  \tdetlint/cmd/importlint\t0.017s\nok  \tdetlint/cmd/nocomments\t0.012s\nok  \tdetlint/cmd/paniccheck\t0.011s\n?   \tdetlint/internal/report\t[no test files]\n",
      "G1-paniccheck.log": "",
      "G8-gen-docs.log": "gen-docs.sh: not built yet\n",
      "G6-tier-r.log": "tier-r.sh: not built yet\n",
      "G2-comments.log": "NOTE: comments compared with baseline 6a780ec6e5e6860d343e72a058001e6503f9c11e\n"
    }
  }
}
```

Smallest suggested fix: Invalidate the default result before setup validation, or write a refusal result with an invocation identifier and require it at landing.

## Coverage and limits

- At least three independent attacks per numbered claim; the table and executable fixtures are the coverage index. Root and nested-module format/build/vet/tidy are separate cases. All six core/engine adapter restrictions are exercised. The Go race attack relies on the runtime detector; its actual diagnostic is required.
- The suite does not prove absence of other bypasses. It does not exhaust arbitrary Go aliasing, indirect calls, every directive spelling, import graph, platform/build-tag combination, or filesystem race. The boundlint alias/helper escapes are documented limits but violate the broad growth claim.
- No live PostgreSQL, fuzz campaign, simulator or CSV pipeline exists at foundation, so their future algorithms cannot be attacked; marker/failure propagation is exercised instead. GitHub CI was read, not remotely dispatched. Real merge/push effects are deliberately blocked.
- The foundation identity-reuse attack demonstrates gate acceptance using c00-foundation with product code; the separate landing wrong-chunk attack demonstrates that landing trusts the caller/result identity. It does not forge a cryptographic certificate; the gate explicitly assumes honest operators.
- testdata is deliberately skipped by the implementation. The report records this mismatch with the adversarial brief rather than treating a documented exclusion as successful coverage.
- The two initial required `kb search` attempts failed before assigning search IDs (no configured kb location for the installed CLI). No corpus files were read and no feedback ID exists. Project findings remain here, within the brief’s permitted edit scope.
- The brief forbids edits outside this folder, so root STATE.md, docs/knowledge and work-scripts were not changed. This folder’s STATE.md is the handoff checkpoint; the attack script is the reusable artifact. No gate fixes were made.
