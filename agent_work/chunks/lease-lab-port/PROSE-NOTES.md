# Lease-lab port (`lease-lab-port`) — prose notes

Prose pass on branch `work/lease-port`, pre-prose tip `a4bc0f3fd16c5ea4bc8843d3d5a575ee2e503f2b`.
No code, tests' logic, copied lab records, `book/manifest.json`, `PLAN.md`, `STATE.md`, or `docs/c4/` changed.

## What was written

### A. Go comments (`test/e2e/`, comments only)

Only the four Go files touched by this port received comment edits:

- `test/e2e/realenv.go`: added an inline comment in `setupSQL()` explaining why `do_cursor`, `do_role` (using `set_config` on `mg_hostile_role` so `current_user <> session_user` persists after return), and `do_custom` (placeholder setting omitted from `pg_settings`, requiring an explicit `current_setting` read-back) are set up as they are.
- `test/e2e/tierr_test.go`: added inline comments on `dirtyColumns`, `secretReadBack`, `hostileFailed`, `observeOneLease` (why all three checks run in one transaction on the immediately following lease with no intervening `DISCARD ALL`), and `(*sessionResult).observe` (why broken or truncated rows count as unreadable rather than movement). Updated the doc comments on `TestTierRSessionStateNeverCrossesClients` and `TestTierRSessionAblationWithoutResetCrosses` to reflect all eight hostile kinds, all seven `dirty-check.sql` columns, `SHOW search_path`, secret read-back, and the zero-unreadable ablation guard.
- `test/e2e/c07_tierr_test.go`: added inline comments on `directBaseline` (matching the `ZT` status of `observeOneLease`) and the `(round+i)%2` parity check in `policyRun` (ensuring an even count of eight hostile kinds pairs every kind with both tenants across two rounds). Updated the doc comment on `TestTierRPoolSessionStateUnderEachReleasePolicy`.
- `test/e2e/sessionoracle_test.go`: added doc comments on all three unit tests (`TestSessionOracleCreditsOnlyCompleteDifferingRows`, `TestSessionOracleCountsABrokenLeaseAsDirtyAndUnreadable`, `TestHostileCallFailsUnlessItAnsweredCleanly`).

### B. Book chapter

Not applicable per brief: this is a port of test code and records, not a new chunk. No chapter was added and `book/manifest.json` was not modified.

### C. Reader-facing pages

- `docs/knowledge/lease-experiment-method.md`: rewritten in Clarity rewrite mode. Every heading, fact, number, formula, caveat, and repository path from the draft was preserved with the same force (`12%`, `2×`, `1.6–2.9 ms`, `195/s`, `235/s`, `8–64`, `14 ms`, `5.5 ms`, `4 vCPUs`, `177 µs`, `104 µs`, `88 µs`, `5 ms`, `5.3 ms`, `109%`, `h0`, `r`, `cap / (...)`, `79.7%`, `79.1–79.4%`, `73.9%`, DuckDB `1.5.x`, and all file pointers under `agent_work/chunks/c05-classifier/lease-exp/`, `agent_work/chunks/lease-lab-port/NOTES.md`, `test/oracles/dirty-check.sql`, and `test/e2e/tierr_test.go`).
- `book/chapters/06-walking-skeleton.md`: updated only the Tier R row of the ablation table (line 151) from five to eight PL/pgSQL functions, listing all seven columns of `test/oracles/dirty-check.sql` (`prepared`, `cursors`, `listens`, `adv_locks`, `session_gucs`, `temp_rels`, `role_changed`), `SHOW search_path`, and the `mg_hostile.secret` read-back, with the verified 24-lease / 69-dirty-observation counts from `agent_work/chunks/lease-lab-port/NOTES.md` and `REVIEW-2.md`. No other line was changed.
- `book/chapters/07-pool.md`: updated only the sentence describing `TestTierRPoolSessionStateUnderEachReleasePolicy` (line 85) to list all eight hostile functions, the single-transaction next-lease read-back of `mg_hostile.secret`, all seven columns of `test/oracles/dirty-check.sql`, and `SHOW search_path`. No other line was changed.

## Narrowed or omitted claims

- In `book/chapters/07-pool.md`, the verbatim Chunk 7 shell output block (lines 87–95) and its following sentence referencing `(4 of 4)` (line 97) were left untouched. The brief restricts edits in that chapter to the sentence listing the planted kinds and checked columns and instructs not to run Docker tier R on this loaded host; `NOTES.md` and `REVIEW-2.md` record `same-tenant carry-over 8` and `always-reseed` `reseeds 36`, but do not record the updated `switches` count for the `on-switch` arms, so updating the quoted transcript would require inventing an unverified number.
- In `book/chapters/06-walking-skeleton.md`, the lead-in sentence above the table (line 146, citing Chunk 6's `NOTES.md`) was left untouched per the brief's single-sentence scope rule.
- No review, repair, or adversarial history was written into any published page or code comment.

## Commands run

All commands were run in `/root/Software/mini-gateway-work/lease-port` with `PATH=/usr/local/go/bin:$PATH`:

1. `gofmt -l .` — empty output (exit 0).
2. `go build ./...` — clean (exit 0).
3. `go vet ./...` — clean (exit 0).
4. `go test -short -count=1 -timeout 5m ./test/e2e/` — `ok github.com/nickstrad/mini-gateway/test/e2e 57.055s` (exit 0).
5. `scripts/site.sh /tmp/opencode/lease-port-site` — `built 27 pages into /tmp/opencode/lease-port-site` (exit 0); confirmed `book/06-walking-skeleton.html` and `book/07-pool.html` built cleanly, then removed the scratch output directory.
6. `astdiff` (`cd tools/detlint && go run ./cmd/astdiff <archive of a4bc0f3> .`) — empty output (exit 0), confirming Go changes are strictly comments only.
