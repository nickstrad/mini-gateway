# CR-2 — adversarial record (Astra, gpt-6-astra, against the Neon rebuild)

Astra wrote the harness (21 attacks on the six claims) against the build tip `b4ede4c` and
reported; the orchestrator re-ran it with the same counts.
First pass: **HELD 17, BROKEN 4**.

After the one repair round (a fresh Neon agent, tip `7813005`), re-run by the orchestrator:
**20 attacks pass, 1 settled by the orchestrator**.

Fixed product defects, all in the corpus check that blocks a retry (`e6e8b17`): a corpus file
rewritten in place, a new file behind a symlinked directory, and a directory whose name
holds a newline each let a retry through. The rule is now: any difference in the target's
corpus tree between before and after the first attempt blocks the retry — files added,
removed or changed in content, listed NUL-delimited through symlinks with a hash per file —
and a snapshot that cannot be taken blocks it too.

## Narrowed claims (accepted by the orchestrator)

- **Claim 2** (`TestAdv_2_WidenedStandaloneBudget`, skipped with a pointer here). As written:
  at most two targets are retried per run. As narrowed: at most two per run that counts for
  landing; a standalone run may set `FUZZ_MAX_RETRIES` above 2 and is then never eligible
  for landing (`scripts/gate.sh` records `counts_for_landing: false`; held by
  `TestGateShLandingCapBlocksWidenedMaxRetriesAlone`).

## Declined

- Review 1 finding 5 (the signature does not compare the `--- FAIL` duration with
  `FUZZ_SECONDS`): declined by the repairer; it guards only against a `TestMain` that forges
  Go's own output. Left to review 2 to accept or not.
