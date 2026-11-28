# E0 review 2 — Fable, 2026-11-26 — PASS

Reviewed at `9642b75` (repair commits `e433e93`, `c9e9b50`, `9642b75`).

- Finding 1 closed. M2I gives each kind its own sessions and adds a `none` control; M2H
  compares backends with and without a temp table in their past. Recomputed from the three
  new runs' `samples.csv`: every kind without a temp table resets within 10 µs of the control
  (133–186 µs against 177 µs); temp table 3.07 ms, with rows and an index 3.89 ms; a backend
  that once held a temp table pays 140 µs against 114 µs on every later clean reset.
  Verification rows: 15 of 15 ok in each run. The old rows are kept as `M2R`, labelled
  order-confounded.
- Finding 2 closed: `probe.out` committed for the first three runs; new runs write `probe.log`.
- The three original run directories' csv and env files are unchanged; calibration.json
  regenerates byte-for-byte from all six directories; build, vet and gofmt are clean.

Left open, stated in NOTES.md: the `none` control itself falls from 177 µs to about 104 µs
over five consecutive resets, so the reset cost depends on how hot the backend is; the first
of the three new runs read higher than the other two.
