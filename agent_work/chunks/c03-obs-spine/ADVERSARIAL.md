# Chunk 3 — adversarial record (Astra, gpt-6-astra)

First pass, against the build tip: **HELD 22, BROKEN 14** —
`agent_work/adversarial/c03-obs-spine/RESULTS.md`.

After the one repair round (orchestrator's run, 2026-11-25,
`go test -count=1 -v ./agent_work/adversarial/c03-obs-spine/...`): **33 attacks pass, 0
fail, 4 settled by the orchestrator** (each carries a `t.Skip` naming this file).

Of the 14 breaks: 10 were product defects and are fixed with regression tests (carriage
returns in labels, duplicate and fabricated CSV rows passing the round trip, the byte cap
not counting the first flush's header, a metric missing in one of two runs passing the
assertion runner, an empty numeric value, counter overflow on merge, two merge attacks with
undeclared points, zero rows for metrics with non-tenant labels).

## Settled by the orchestrator

- `TestAdv_8_CounterGaugeFamilyCollision`, `TestAdv_8_HistogramGaugeSeriesCollision` —
  **fixed another way.** The registry now refuses declarations whose Prometheus names would
  collide, so the attack's setup is rejected before it can produce colliding output. The
  claim (Prometheus output always parses) holds.
- `TestAdv_2_PrometheusHistoryIndependence` — **claim 2 narrowed.** As written: "equal
  inputs give byte-identical outputs". As narrowed: an adapter's output is a deterministic
  function of its whole input history; the Prometheus adapter evicts the label sets of
  tenants that went quiet, so two adapters fed the same snapshot after different histories
  can differ. Equal histories give byte-identical output.
- `TestAdv_12_RuntimeDeclarationDocumentationGap` — **claim 12 narrowed.** As written: a
  metric cannot be exported without being declared and documented. As narrowed: it cannot
  be exported without being declared; documentation in `docs/csv-schema.md` is enforced for
  every source listed in the catalog (`internal/obs/catalog`). A source registered at run
  time outside the catalog is declared but undocumented. Follow-up for chunk 6: a test that
  every source the real binary registers is in the catalog.
