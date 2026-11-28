-- Table 7b: the warm target against the offered load over time (profile `step`; table 7's
-- second part).
-- Parameters:
SET VARIABLE step_profile = coalesce(getvariable('step_profile'), 'step');
-- Reads: metrics.csv, every flush, of readable runs with warm.mode=dynamic and
--   workload.profile = step_profile.
-- Keys: run directory (run id), run.arm, flush.
-- Formulas: per flush, offered/s = (leases_requested - previous flush's) / (t - previous t);
--   warm_target, warm, lambda_hat_milli and lead_hat_ns are the gauges at the flush.
--   The first flush (t = 0) has no rate. Resolution = obs.interval of the run.
WITH f AS (
  SELECT m.dir, m.flush_seq, any_value(m.t_ns) AS t_ns,
    sum(value) FILTER (WHERE metric = 'leases_requested') AS requested,
    max(value) FILTER (WHERE metric = 'warm_target') AS warm_target,
    max(value) FILTER (WHERE metric = 'warm') AS warm,
    max(value) FILTER (WHERE metric = 'lambda_hat_milli') AS lambda_hat_milli,
    max(value) FILTER (WHERE metric = 'lead_hat_ns') AS lead_hat_ns
  FROM m JOIN ok_runs r USING (dir)
  WHERE r.warm_mode = 'dynamic' AND r.profile = getvariable('step_profile')
  GROUP BY m.dir, m.flush_seq)
SELECT r.tier, r.campaign, r.test, r.arm, r.run_id, f.flush_seq, f.t_ns,
  round((f.requested - lag(f.requested) OVER w) * 1e9 / nullif(f.t_ns - lag(f.t_ns) OVER w, 0), 1) AS offered_per_s,
  f.warm_target, f.warm, f.lambda_hat_milli, f.lead_hat_ns
FROM f JOIN ok_runs r USING (dir)
WINDOW w AS (PARTITION BY f.dir ORDER BY f.flush_seq)
ORDER BY r.tier, r.campaign, r.test, r.arm, r.run_id, f.flush_seq;
