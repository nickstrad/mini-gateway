-- Table 3: cost per lease — reset against remake, quantiles.
-- Reads: histograms.csv (last flush) of every readable run: reset_ns, connect_ns,
--   close_lag_ns, first_stmt_ns by mode (fresh, reused, after_reset); metrics.csv for the
--   clamped counts.
-- Keys: run.tier, run.campaign, run.test, release.policy, warm.mode, warm.prime, metric, mode.
-- Formulas: buckets summed over the group's runs; quantile q = the upper edge of the bucket
--   holding the ceil(q*n)-th value; res_ppm = that bucket's (hi - lo) / lo, the resolution
--   error of the quantile beside it. clamped_low / clamped_high = values outside 1 us - 60 s
--   (clamped-low values sit in the lowest bucket, read "<= 1 us").
WITH g AS (
  SELECT r.tier, r.campaign, r.test, r.policy, r.warm_mode, r.prime, h.metric, h.mode, h.lo, h.hi, sum(h.c) AS c,
         count(DISTINCT h.dir) AS runs
  FROM fh h JOIN ok_runs r USING (dir)
  WHERE h.metric IN ('reset_ns', 'connect_ns', 'close_lag_ns', 'first_stmt_ns')
  GROUP BY ALL),
l AS (SELECT tier, campaign, test, policy, warm_mode, prime, metric, mode, list({'lo': lo, 'hi': hi, 'c': c} ORDER BY lo) AS b, sum(c) AS n
      FROM g GROUP BY ALL),
cl AS (SELECT r.tier, r.campaign, r.test, r.policy, r.warm_mode, r.prime, regexp_replace(f.metric, '_clamped_(low|high)$', '') AS metric, f.mode,
              count(DISTINCT f.dir) AS runs,
              sum(f.value) FILTER (WHERE f.metric LIKE '%_clamped_low') AS clamped_low,
              sum(f.value) FILTER (WHERE f.metric LIKE '%_clamped_high') AS clamped_high
       FROM fm f JOIN ok_runs r USING (dir)
       WHERE f.metric SIMILAR TO '(reset_ns|connect_ns|close_lag_ns|first_stmt_ns)_clamped_(low|high)'
       GROUP BY ALL),
q AS (SELECT *, lr_hq(b, 0.5) AS q50, lr_hq(b, 0.9) AS q90, lr_hq(b, 0.99) AS q99 FROM l)
SELECT cl.tier, cl.campaign, cl.test, cl.policy, cl.warm_mode, cl.prime, cl.metric, cl.mode, cl.runs, coalesce(q.n, 0) AS n,
  q.q50.hi AS p50_ns, lr_res_ppm(q.q50) AS p50_res_ppm, q.q90.hi AS p90_ns, lr_res_ppm(q.q90) AS p90_res_ppm,
  q.q99.hi AS p99_ns, lr_res_ppm(q.q99) AS p99_res_ppm, cl.clamped_low, cl.clamped_high
FROM cl LEFT JOIN q USING (tier, campaign, test, policy, warm_mode, prime, metric, mode)
ORDER BY ALL;
