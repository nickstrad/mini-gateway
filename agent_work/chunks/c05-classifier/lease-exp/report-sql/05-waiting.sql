-- Table 5: waiting — lease_wait_ns and switch_ready_ns quantiles per policy and load, the
-- baseline beside each, ratio; refusals beside every quantile (a refused lease is in no
-- histogram).
-- Reads: histograms.csv (last flush) and metrics.csv of every readable run.
-- Keys: run.tier, run.campaign, run.test, workload, workload.load, release.policy, run.arm,
--   metric (lease_wait_ns over every outcome; switch_ready_ns over modes warm, connect, reset,
--   ready, i.e. leases whose connection last served another tenant or none).
-- Formulas: per run, quantile q = upper edge of the bucket holding the ceil(q*n)-th value;
--   then across the group's runs (seeds / run.rep): the median of each quantile (integer ns), and the min
--   and max of p99 (the spread). p99_res_ppm = largest bucket width / lo among the runs' p99.
--   baseline_* = the same for the group's baseline arm (none + cold); p99_ratio = p99 / baseline
--   p99, empty when the baseline's p99 sits in the histogram's lowest bucket (median lo <= 1000 ns,
--   i.e. <= 1 us: a ratio to a clamped value says nothing). refused = sum of leases_refused; refused_frac = refused / requested.
--   n = values in the histograms (granted leases for lease_wait_ns).
WITH q AS (
  SELECT r.tier, r.campaign, r.test, r.workload, r.load, r.policy, r.arm, q.metric, q.dir,
    q.n, q.q50.hi AS p50, q.q90.hi AS p90, q.q99.hi AS p99, q.q99.lo AS p99_lo, q.q999.hi AS p999, lr_res_ppm(q.q99) AS p99_res
  FROM run_q q JOIN ok_runs r USING (dir)),
g AS (
  SELECT tier, campaign, test, workload, load, policy, arm, metric, count(*) AS runs, sum(n) AS n,
    median(p50)::BIGINT AS p50_ns, median(p90)::BIGINT AS p90_ns, median(p99)::BIGINT AS p99_ns, median(p999)::BIGINT AS p999_ns,
    min(p99) AS p99_min_ns, max(p99) AS p99_max_ns, max(p99_res) AS p99_res_ppm, median(p99_lo) AS p99_lo_ns
  FROM q GROUP BY ALL),
ref AS (
  SELECT tier, campaign, test, workload, load, arm, sum(refused) AS refused, sum(requested) AS requested
  FROM ok_runs GROUP BY ALL)
SELECT g.tier, g.campaign, g.test, g.workload, g.load, g.policy, g.arm, g.metric, g.runs, g.n,
  g.p50_ns, g.p90_ns, g.p99_ns, g.p999_ns, g.p99_min_ns, g.p99_max_ns, g.p99_res_ppm,
  ref.refused, round(ref.refused / ref.requested, 6) AS refused_frac,
  bg.p50_ns AS baseline_p50_ns, bg.p99_ns AS baseline_p99_ns, bref.refused AS baseline_refused,
  CASE WHEN bg.p99_lo_ns > 1000 THEN round(g.p99_ns / bg.p99_ns, 4) END AS p99_ratio
FROM g JOIN ref USING (tier, campaign, test, workload, load, arm)
LEFT JOIN base_arms ba USING (tier, campaign, test, workload, load)
LEFT JOIN g bg ON bg.tier = g.tier AND bg.campaign = g.campaign AND bg.test = g.test AND bg.workload = g.workload
  AND bg.load = g.load AND bg.metric = g.metric AND bg.arm = ba.base_arm
LEFT JOIN ref bref ON bref.tier = g.tier AND bref.campaign = g.campaign AND bref.test = g.test AND bref.workload = g.workload
  AND bref.load = g.load AND bref.arm = ba.base_arm
ORDER BY g.tier, g.campaign, g.test, g.workload, g.load, g.metric, g.policy, g.arm;
