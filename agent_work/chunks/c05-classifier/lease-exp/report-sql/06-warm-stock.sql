-- Table 6: warm stock — miss rate against W, measured beside P(Poisson(m) >= W), with a
-- binomial band.
-- Reads: metrics.csv (last flush) and lab.csv of readable runs with a remake policy and
--   warm.mode cold (W = 0) or fixed (W = warm.size); dynamic runs are table 7's.
-- Keys: run.tier, run.campaign, run.test, workload, workload.load, release.policy,
--   remake.patience, warm.mode, W.
-- Formulas (views.sql run_miss; REVIEW-E2-2 finding 2), counts summed over the group's runs:
--   misses = stock_empty_free_slot;  n = switch_arrivals - (stock_empty_arrivals -
--   stock_empty_free_slot) (arrivals that found no free slot are outside the model);
--   measured = misses / n;  lambda = n / total arrival window;  L = sum connecting_ns_total /
--   sum connects{started};  m = lambda x L;  predicted p = P(Poisson(m) >= W) = 1 - sum_{k<W}
--   e^-m m^k / k!;  band = p +- band_z x sqrt(p (1 - p) / n) (normal approximation to the
--   binomial, clipped to [0, 1]); side = below / inside / above the band. miss_slot is not a
--   miss (patient waiters); switch_arrivals is fresh-connection demand, not tenant switches.
WITH g AS (
  SELECT r.tier, r.campaign, r.test, r.workload, r.load, r.policy, r.patience_label AS patience, r.warm_mode, w.w,
    count(*) AS runs, sum(w.misses) AS misses, sum(w.n) AS n, sum(r.connecting_ns) AS connecting_ns,
    sum(r.connects_started) AS connects_started, sum(r.duration_ns) AS window_ns
  FROM run_miss w JOIN ok_runs r USING (dir)
  WHERE r.warm_mode IN ('cold', 'fixed')
  GROUP BY ALL),
p AS (
  SELECT *, n * 1e9 / window_ns AS lambda_per_s, connecting_ns / nullif(connects_started, 0) AS l_ns
  FROM g),
pp AS (
  SELECT *, lambda_per_s * l_ns / 1e9 AS m FROM p),
b AS (
  SELECT *, lr_poisson_tail(m, w) AS predicted FROM pp)
SELECT tier, campaign, test, workload, load, policy, patience, warm_mode, w, runs, misses, n,
  round(misses / nullif(n, 0), 6) AS measured, round(lambda_per_s, 3) AS lambda_per_s, round(l_ns) AS connect_mean_ns,
  round(m, 4) AS m, round(predicted, 6) AS predicted,
  round(greatest(0, predicted - getvariable('band_z') * sqrt(predicted * (1 - predicted) / nullif(n, 0))), 6) AS band_lo,
  round(least(1, predicted + getvariable('band_z') * sqrt(predicted * (1 - predicted) / nullif(n, 0))), 6) AS band_hi,
  CASE WHEN n IS NULL OR n <= 0 OR m IS NULL THEN 'no data'
       WHEN misses / n < predicted - getvariable('band_z') * sqrt(predicted * (1 - predicted) / n) THEN 'below'
       WHEN misses / n > predicted + getvariable('band_z') * sqrt(predicted * (1 - predicted) / n) THEN 'above'
       ELSE 'inside' END AS side
FROM b
ORDER BY tier, campaign, test, workload, load, policy, patience, warm_mode, w;
