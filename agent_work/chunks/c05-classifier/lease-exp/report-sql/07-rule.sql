-- Table 7: the rule — R1 (warm.mode=dynamic) against each fixed W and against hindsight-fixed.
-- Reads: metrics.csv (last flush) and lab.csv of readable remake runs.
-- Keys: run.tier, run.campaign, run.test, workload, workload.load, seed, run.rep,
--   release.policy, remake.patience; one row per run (fixed W or dynamic).
-- Formulas: miss rate as table 6 (stock_empty_free_slot / (switch_arrivals -
--   (stock_empty_arrivals - stock_empty_free_slot))); warm_conn_s = warm_ns_total / 1e9;
--   epsilon = warm.epsilon of the comparison's dynamic run; hindsight W = the smallest fixed W
--   whose miss rate <= epsilon on the same seed, rep and workload; ratio = warm_conn_s /
--   hindsight-fixed warm_conn_s; holds = miss rate <= epsilon. Empty hindsight = no fixed W held
--   epsilon (or none was run).
WITH r AS (
  SELECT o.tier, o.campaign, o.test, o.workload, o.load, o.seed, o.rep, o.policy, o.patience_label AS patience,
    o.warm_mode, o.arm, w.w, w.misses, w.n, w.misses / nullif(w.n, 0) AS miss_rate, w.warm_conn_s, o.epsilon
  FROM run_miss w JOIN ok_runs o USING (dir)
  WHERE o.warm_mode IN ('fixed', 'dynamic')),
e AS (SELECT tier, campaign, test, workload, load, seed, rep, policy, patience, max(epsilon) AS eps
      FROM r WHERE warm_mode = 'dynamic' GROUP BY ALL),
h AS (
  SELECT r.tier, r.campaign, r.test, r.workload, r.load, r.seed, r.rep, r.policy, r.patience,
    arg_min(r.w, r.w) AS hindsight_w, arg_min(r.warm_conn_s, r.w) AS hindsight_warm_conn_s
  FROM r JOIN e USING (tier, campaign, test, workload, load, seed, rep, policy, patience)
  WHERE r.warm_mode = 'fixed' AND r.miss_rate <= e.eps
  GROUP BY ALL)
SELECT r.tier, r.campaign, r.test, r.workload, r.load, r.seed, r.rep, r.policy, r.patience, r.warm_mode, r.arm,
  CASE WHEN r.warm_mode = 'fixed' THEN r.w END AS w, r.misses, r.n, round(r.miss_rate, 6) AS miss_rate,
  round(r.warm_conn_s, 3) AS warm_conn_s, e.eps AS epsilon, r.miss_rate <= e.eps AS holds_epsilon,
  h.hindsight_w, round(h.hindsight_warm_conn_s, 3) AS hindsight_warm_conn_s,
  round(r.warm_conn_s / nullif(h.hindsight_warm_conn_s, 0), 4) AS ratio_to_hindsight
FROM r LEFT JOIN e USING (tier, campaign, test, workload, load, seed, rep, policy, patience)
LEFT JOIN h USING (tier, campaign, test, workload, load, seed, rep, policy, patience)
ORDER BY r.tier, r.campaign, r.test, r.workload, r.load, r.seed, r.rep, r.policy, r.patience, r.warm_mode = 'dynamic', r.w;
