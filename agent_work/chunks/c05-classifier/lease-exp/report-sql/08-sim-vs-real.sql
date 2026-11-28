-- Table 8: simulator against real — the paired runs' hit rate, wait quantiles and goodput.
-- Parameters:
SET VARIABLE sim_real_band_pct = coalesce(getvariable('sim_real_band_pct'), 25);  -- declared band: |real/sim - 1| <= this %
-- Reads: metrics.csv, histograms.csv (last flush) and lab.csv of readable runs of both tiers.
-- Keys: run.campaign, run.test, run.arm, workload.load — a pair is the same four labels in
--   tier sim and tier real (R8 runs the same config and seed in both). Every key with a real
--   run is listed; a missing side is `missing`.
-- Formulas, per tier over the key's runs: hit_rate = 1 - sum(stock_empty_free_slot) / sum(n)
--   (table 6's n; remake policies only); p50/p99_wait_ns = median over runs of the run's
--   lease_wait_ns quantile (bucket upper edge); goodput_per_s = median of leases_goodput /
--   run.duration (arrival window). ratio = real / sim; inside = |ratio - 1| <= sim_real_band_pct / 100.
WITH r AS (
  SELECT o.tier, o.campaign, o.test, o.arm, o.load, o.goodput * 1e9 / o.duration_ns AS goodput_per_s,
    q.q50.hi AS p50, q.q99.hi AS p99, w.misses, w.n
  FROM ok_runs o LEFT JOIN run_q q ON q.dir = o.dir AND q.metric = 'lease_wait_ns' LEFT JOIN run_miss w ON w.dir = o.dir),
t AS (
  SELECT tier, campaign, test, arm, load, count(*) AS runs, 1 - sum(misses) / nullif(sum(n), 0) AS hit_rate,
    median(p50)::DOUBLE AS p50_wait_ns, median(p99)::DOUBLE AS p99_wait_ns, median(goodput_per_s) AS goodput_per_s
  FROM r GROUP BY ALL),
u AS (UNPIVOT t ON hit_rate, p50_wait_ns, p99_wait_ns, goodput_per_s INTO NAME quantity VALUE v),
k AS (SELECT DISTINCT campaign, test, arm, load, quantity FROM u WHERE tier = 'real')
SELECT k.campaign, k.test, k.arm, k.load, k.quantity, s.runs AS sim_runs, re.runs AS real_runs,
  round(s.v, 6) AS sim, round(re.v, 6) AS real, round(re.v / nullif(s.v, 0), 4) AS ratio,
  getvariable('sim_real_band_pct') AS band_pct,
  CASE WHEN s.v IS NULL THEN 'missing sim' WHEN re.v IS NULL THEN 'missing real'
       WHEN s.v = 0 AND re.v = 0 THEN 'inside' WHEN s.v = 0 THEN 'outside'
       WHEN abs(re.v / s.v - 1) <= getvariable('sim_real_band_pct') / 100.0 THEN 'inside' ELSE 'outside' END AS agreement
FROM k LEFT JOIN u s ON s.tier = 'sim' AND s.campaign = k.campaign AND s.test = k.test AND s.arm = k.arm AND s.load = k.load AND s.quantity = k.quantity
LEFT JOIN u re ON re.tier = 'real' AND re.campaign = k.campaign AND re.test = k.test AND re.arm = k.arm AND re.load = k.load AND re.quantity = k.quantity
ORDER BY ALL;
