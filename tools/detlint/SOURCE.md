# Where these tools came from

Copied on 2026-11-24 from the sister project `object-store`:

- Repository: `github.com/nickstrad/object-store` (checkout `/root/Software/object-store`)
- Commit: `060bb3edc11118d525f1239185c7f6c739bb9c24`
- Path there: `agent_work/gates/detlint/` (the working tree was clean for that path)

Copied unchanged: `detlint` (`main.go`, `detlint.go`, `load.go`, tests, `testdata/`), and the
commands `astdiff`, `nocomments`, `importcheck`, `paniccheck` with their tests and testdata.

Changed for the move, and nothing else:

| File | Change |
| --- | --- |
| `internal/report/report.go` | `SkipDir`: the skipped directory names are now `agent_work`, `.git`, `testdata`, `vendor`, `tools` (was `agent_work`, `kb`, `.git`, `testdata`, `vendor`). `nocomments`, `astdiff` and `paniccheck` share this list. |
| `cmd/nocomments/main.go` | The comment naming those directories. |
| `cmd/nocomments/testdata/clean/kb/` | Renamed to `testdata/clean/tools/` so the test still proves a skipped directory is skipped. |
| `README-tools.txt` | Build and example commands use this repository's paths and the host toolchain (no container); the skip list; new sections 6 and 7. |

Written here, not copied: `cmd/importlint` and `cmd/boundlint` (README-tools.txt sections 6 and 7).
