# Chunk 6a — adversarial record (Astra, gpt-6-astra)

Astra wrote the harness (45 cases) against the build tip `cd3513b` and reported.
First pass: **HELD 36, BROKEN 9**.

After the one repair round (tip `118a5d7`), re-run by the orchestrator:
**44 pass, 1 settled by the orchestrator**. No claim was narrowed.

Fixed product defects:

- `mgctl query` named the read-loss row of the error contract (retry: yes, connection stays
  usable) for a connection lost inside a transaction (`FATAL 08006`); also review 1 finding 1.
- A password line over 1,024 bytes on stdin was silently cut; it is refused now, in
  `mgctl verifier`, `mgctl query` and `loadgen`.
- The report passed an empty run directory given beside a good one, and a run whose
  `metrics.csv` held fewer flushes than `run.csv` declares.
- `scripts/chaos.sh` printed all green when the test process failed after a passing seed,
  and when a parent scenario failed after its seeds passed.
- `make run-down` reported success when Docker could not list the stack's resources.
- `FormatNs` printed the largest duration as `0.0s` (overflow before the division).

## Settled

- `TestAdv_3_ZeroIntendedShortfall` (skipped with a pointer here): a plan of zero operations
  printed no SHORTFALL line. Such a plan is now refused at validation, so the case cannot be
  built; the test stops at its own setup.
