-- Table 4: capacity — goodput against offered load per policy, ratio to the baseline on the
-- same seed, predicted knee beside the measured one.
-- Parameters:
SET VARIABLE knee_tol_pct = coalesce(getvariable('knee_tol_pct'), 2);  -- a load is past the knee when goodput < (100 - this)% of the leases requested
-- Reads: metrics.csv (last flush) and lab.csv of every readable run; calibration.json.
-- Keys: run.tier, run.campaign, run.test, workload (profile, tenants, skew, lease, statement
--   class, script, cap, duration), release.policy, run.arm, workload.load. Baseline = the
--   group's none + cold arm (views.sql base_arms); pairs share seed and run.rep.
-- Formulas, per run, then the median over the group's runs (seeds / repetitions):
--   offered/s = requests.achieved / run.duration;  goodput/s = leases_goodput / run.duration
--   (the arrival window; leases drained after it count); goodput_frac = leases_goodput /
--   requests.achieved; throughput/s = leases_goodput / run.end_ns (arrival window + drain:
--   a backlog served after the window lowers it); ratio = goodput / baseline goodput, same seed and rep; S = baseline's lent_ns_total / granted (mean connection hold per lease);
--   f = tenant switches / granted (leases_switch modes warm, connect, reset, ready);
--   r = calibrated reset (M2I after a statement), Lx = calibrated connect (M3 c=1) + close (M4);
--   C = connect ceiling: real = calibration M7S (ceiling_cond); sim = cm.cores x avail_pm/1000
--       / (cm.work + cm.close_work) (the run's connect model).
--   predicted capacity/s: none cap/S; reset-always cap/(S+r); reset-on-switch cap/(S+f r);
--     remake-always min(cap/(S+Lx), C); remake-on-switch min(cap/(S+f Lx), C/f).
--   predicted knee (% load) = predicted / (cap / mean lease time) x 100 (the generator's 100%).
--   measured knee: the arm's lowest load whose median goodput_frac < (100 - knee_tol_pct)%
--   (knee_below_load), and the highest load under it that is not (knee_above_load); empty
--   below = no tested load was past the knee.
WITH r AS (
  SELECT o.*, achieved * 1e9 / duration_ns AS offered_per_s, goodput * 1e9 / duration_ns AS goodput_per_s, goodput * 1e9 / nullif(end_ns, 0) AS throughput_per_s,
    goodput / nullif(achieved, 0) AS goodput_frac,
    lent_ns / nullif(granted, 0) AS hold_ns, switches / nullif(granted, 0) AS switch_frac,
    CASE lease_dist WHEN 'short' THEN 5e5 ELSE 5e6 END AS s_nom_ns,
    CASE WHEN tier = 'real' THEN (SELECT ceiling_milli / 1000.0 FROM cal)
         ELSE cm_cores_run_n * coalesce(TRY_CAST(cm_avail_pm AS DOUBLE), 1000) / 1000 * 1e9 / nullif(cm_work_run_ns + cm_close_work_run_ns, 0) END AS ceiling_per_s
  FROM ok_runs o),
b AS (SELECT r.tier, r.campaign, r.test, r.workload, r.load, r.seed, r.rep, r.goodput_per_s, r.hold_ns
      FROM r JOIN base_arms ba USING (tier, campaign, test, workload, load) WHERE r.arm = ba.base_arm),
p AS (SELECT r.*, b.goodput_per_s AS base_goodput_per_s, b.hold_ns AS base_hold_ns
      FROM r LEFT JOIN b USING (tier, campaign, test, workload, load, seed, rep)),
g AS (
  SELECT tier, campaign, test, workload, policy, arm, load, any_value(cap) AS cap, any_value(s_nom_ns) AS s_nom_ns,
    count(*) AS runs, sum(refused) AS refused, median(offered_per_s) AS offered_per_s, median(goodput_per_s) AS goodput_per_s, median(throughput_per_s) AS throughput_per_s, median(goodput_frac) AS goodput_frac,
    median(goodput_per_s / base_goodput_per_s) AS goodput_ratio, count(base_goodput_per_s) AS paired_runs,
    median(base_hold_ns) AS s_ns, median(switch_frac) AS f, median(ceiling_per_s) AS ceiling_per_s
  FROM p GROUP BY ALL),
pr AS (
  SELECT g.*, cal.reset_ns AS r_ns, cal.connect_ns + cal.close_ns AS lx_ns,
    CASE policy
      WHEN 'none' THEN cap * 1e9 / s_ns
      WHEN 'reset-always' THEN cap * 1e9 / (s_ns + cal.reset_ns)
      WHEN 'reset-on-switch' THEN cap * 1e9 / (s_ns + f * cal.reset_ns)
      WHEN 'remake-always' THEN least(cap * 1e9 / (s_ns + cal.connect_ns + cal.close_ns), ceiling_per_s)
      WHEN 'remake-on-switch' THEN least(cap * 1e9 / (s_ns + f * (cal.connect_ns + cal.close_ns)), ceiling_per_s / nullif(f, 0)) END AS predicted_per_s
  FROM g, cal),
k AS (
  SELECT *, goodput_frac < (100 - getvariable('knee_tol_pct')) / 100.0 AS past_knee FROM pr),
kn AS (
  SELECT tier, campaign, test, workload, arm, min(load) FILTER (WHERE past_knee) AS knee_below_load FROM k GROUP BY ALL)
SELECT k.tier, k.campaign, k.test, k.workload, k.policy, k.arm, k.load, k.runs,
  round(k.offered_per_s, 1) AS offered_per_s, round(k.goodput_per_s, 1) AS goodput_per_s, round(k.throughput_per_s, 1) AS throughput_per_s,
  round(k.goodput_frac, 6) AS goodput_frac,
  round(k.goodput_ratio, 6) AS goodput_ratio_to_baseline, k.paired_runs, k.refused,
  round(k.f, 6) AS switch_fraction, round(k.s_ns) AS baseline_hold_ns,
  round(k.predicted_per_s, 1) AS predicted_capacity_per_s,
  round(100 * k.predicted_per_s * k.s_nom_ns / 1e9 / k.cap, 1) AS predicted_knee_load,
  (SELECT max(load) FROM k k2 WHERE k2.tier = k.tier AND k2.campaign = k.campaign AND k2.test = k.test AND k2.workload = k.workload
     AND k2.arm = k.arm AND NOT k2.past_knee AND (kn.knee_below_load IS NULL OR k2.load < kn.knee_below_load)) AS knee_above_load,
  kn.knee_below_load
FROM k JOIN kn USING (tier, campaign, test, workload, arm)
ORDER BY k.tier, k.campaign, k.test, k.workload, k.policy, k.arm, k.load;
