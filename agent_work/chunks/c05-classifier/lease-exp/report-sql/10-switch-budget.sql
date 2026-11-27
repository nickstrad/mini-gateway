-- Table 10: switch-rate budget (Nick's ruling of 2026-11-25) — tenant switches per second as a
-- fraction of the connect ceiling, and the CPU that rate costs.
-- Reads: metrics.csv (last flush), lab.csv, cpu.csv of readable runs; calibration.json.
-- Keys: run.tier, run.campaign, run.test, workload, workload.load, release.policy, run.arm.
-- Formulas, per run then the median over the group's runs (seeds / run.rep):
--   switches = leases_switch{warm, connect, reset, ready} (leases whose connection last served
--   another tenant or none; NOT switch_arrivals, which is fresh-connection demand);
--   switches/s = switches / run.duration;  connects/s = connects{started} / run.duration;
--   measured ceiling = calibration M7S (ceiling_cond, ~195/s); model ceiling = cm.cores x
--   avail_pm/1000 / (cm.work + cm.close_work) of the run (~174/s); fraction = switches/s /
--   ceiling; CPU = rate x calibrated Postgres CPU per connect-and-close (M7S median), in CPUs,
--   for switches/s (as if each switch were a remake) and for the run's connects/s;
--   measured Postgres CPU per 1,000 leases = (lab.csv cpu_pg_key, else cpu.csv pg_cpu_ns) /
--   leases_requested x 1000, empty with `not measured` where the run has neither (R9).
WITH r AS (
  SELECT o.*, switches * 1e9 / duration_ns AS switch_per_s, connects_started * 1e9 / duration_ns AS connect_per_s,
    cm_cores_run_n * coalesce(TRY_CAST(cm_avail_pm AS DOUBLE), 1000) / 1000 * 1e9 / nullif(cm_work_run_ns + cm_close_work_run_ns, 0) AS model_ceiling,
    coalesce(o.lab_pg_cpu_ns, c.pg_cpu_ns) * 1000.0 / nullif(requested, 0) AS pg_cpu_per_1000_ns
  FROM ok_runs o LEFT JOIN cpu c USING (dir)),
g AS (
  SELECT tier, campaign, test, workload, load, policy, arm, count(*) AS runs, median(switch_per_s) AS switch_per_s,
    min(switch_per_s) AS switch_per_s_min, max(switch_per_s) AS switch_per_s_max,
    median(connect_per_s) AS connect_per_s, median(model_ceiling) AS model_ceiling,
    median(pg_cpu_per_1000_ns) AS pg_cpu_per_1000_ns, count(pg_cpu_per_1000_ns) AS cpu_runs
  FROM r GROUP BY ALL)
SELECT tier, campaign, test, workload, load, policy, arm, runs,
  round(switch_per_s, 2) AS switches_per_s, round(switch_per_s_min, 2) AS switches_per_s_min, round(switch_per_s_max, 2) AS switches_per_s_max,
  round((SELECT ceiling_milli / 1000.0 FROM cal), 2) AS ceiling_measured_per_s,
  round(switch_per_s / (SELECT ceiling_milli / 1000.0 FROM cal), 4) AS frac_of_measured,
  round(model_ceiling, 2) AS ceiling_model_per_s, round(switch_per_s / model_ceiling, 4) AS frac_of_model,
  round(connect_per_s, 2) AS connects_per_s,
  round(switch_per_s * (SELECT pg_cpu_per_connect_ns FROM cal) / 1e9, 4) AS pg_cpus_if_switches_remake,
  round(connect_per_s * (SELECT pg_cpu_per_connect_ns FROM cal) / 1e9, 4) AS pg_cpus_for_connects,
  round(pg_cpu_per_1000_ns) AS pg_cpu_ns_per_1000_leases, cpu_runs,
  CASE WHEN cpu_runs = 0 THEN 'not measured' WHEN cpu_runs < runs THEN 'measured in ' || cpu_runs || ' of ' || runs || ' runs' ELSE '' END AS cpu_note
FROM g
ORDER BY tier, campaign, test, workload, load, policy, arm;
