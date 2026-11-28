-- Table 9: verdict inputs against plan section 10 — one row per arm and threshold: the measured
-- value, the threshold, and pass / fail / inconclusive. The SQL computes; it does not editorialise.
-- Parameters:
SET VARIABLE verdict_load = coalesce(getvariable('verdict_load'), 80);           -- workload.load of the goodput and p99 lines
SET VARIABLE goodput_min_ratio = coalesce(getvariable('goodput_min_ratio'), 0.95);
SET VARIABLE p99_max_ratio = coalesce(getvariable('p99_max_ratio'), 2);
SET VARIABLE wcs_max_ratio = coalesce(getvariable('wcs_max_ratio'), 1.5);
SET VARIABLE min_valid_reps = coalesce(getvariable('min_valid_reps'), 5);        -- valid runs needed per side (or all run, if fewer)
SET VARIABLE p99_abs_floor_ns = coalesce(getvariable('p99_abs_floor_ns'), 1000000); -- absolute p99 bound when the baseline's p99 is <= 1 us
-- Reads: every run with a lab.csv (readable or not: a broken run of an arm makes its rows
--   inconclusive; a runner-invalid run is dropped and counted), oracle.csv / leases.csv through
--   views.sql `iso`, metrics and histograms.
-- Keys: run.tier, run.campaign, run.test, workload, release.policy, remake.patience, run.arm;
--   threshold. Baseline and A/A partner as views.sql base_arms (none + cold).
-- Formulas and verdicts:
--   isolation      measured = found rows of classes state, canary, structural, identity over all
--                  the arm's runs (table 2; residue excluded); = 0 -> pass, > 0 -> fail; nothing
--                  checked -> inconclusive.
--   goodput        at verdict_load. sim: measured = the smallest, over seeds/reps, of goodput/s
--                  / baseline goodput/s on the same seed and rep; >= goodput_min_ratio -> pass,
--                  else fail. real: medians over the valid run.rep; ratio = arm / baseline,
--                  d = baseline - arm, floor = |A/A arm - baseline|; floor >= (1 -
--                  goodput_min_ratio) x baseline -> inconclusive (the noise is as large as the
--                  threshold); else ratio < goodput_min_ratio or d > 2 floor -> fail; else pass.
--                  The note prints ratio, d against 2 floor, the three medians and the reps.
--   p99_wait       at verdict_load, lease_wait_ns p99 (bucket upper edge). sim: measured = the
--                  largest same-seed ratio arm / baseline; <= p99_max_ratio -> pass, else fail;
--                  inconclusive when the arm's bucket straddles p99_max_ratio x the baseline's
--                  bucket. real: medians over the valid run.rep, measured = arm / baseline,
--                  floor = |A/A p99 - baseline p99|; |arm - p99_max_ratio x baseline| <= 2 floor
--                  -> inconclusive; else arm <= p99_max_ratio x baseline -> pass, else fail.
--                  Clamp bucket (both tiers): where the baseline's p99 sits in the histogram's
--                  lowest bucket (lo <= 1000 ns, i.e. <= 1 us) a ratio says nothing; that
--                  comparison is absolute instead: measured = arm p99 ns, <= p99_abs_floor_ns
--                  -> pass, else inconclusive (both values in the note). Refused leases are in
--                  no histogram: note = refusals.
--   warm_miss      dynamic arms, all loads: measured = sum misses / sum n (table 6); <= epsilon
--                  -> pass, else fail; inconclusive when measured +- band_z x sqrt(p(1-p)/n)
--                  straddles epsilon, or n = 0.
--   warm_conn_s    dynamic arms: measured = the largest, over (load, seed, rep), of warm_conn_s /
--                  hindsight-fixed warm_conn_s (table 7); <= wcs_max_ratio -> pass, else fail;
--                  inconclusive when some comparison has no hindsight W.
--   Any row: a required run missing (no run of the arm, or of the baseline, at the load) or a
--   run of the arm, the baseline or the A/A arm listed in table 0 as 'broken' -> inconclusive,
--   with a note. Runs of severity 'invalid' (the runner's gate, real-tier db_lost) are dropped
--   from every value and counted in the note (valid of total); goodput and p99_wait are
--   inconclusive when the arm, the baseline or (real) the A/A arm has fewer valid runs at the
--   load than least(min_valid_reps, runs of that arm at the load).
WITH
lr AS (SELECT * FROM runs WHERE has_lab AND tier IS NOT NULL AND policy IS NOT NULL),
bad AS (SELECT DISTINCT dir FROM problems WHERE severity = 'broken'),
inv AS (SELECT DISTINCT dir FROM problems WHERE severity = 'invalid' AND dir NOT IN (SELECT dir FROM bad)),
arms AS (SELECT DISTINCT tier, campaign, test, workload, policy, patience_label AS patience, warm_mode, arm FROM lr),
arm_bad AS (SELECT tier, campaign, test, workload, arm, load, count(*) AS n_bad FROM lr WHERE dir IN (SELECT dir FROM bad) GROUP BY ALL),
arm_inv AS (SELECT tier, campaign, test, workload, arm, count(*) AS n_inv FROM lr WHERE dir IN (SELECT dir FROM inv) GROUP BY ALL),
-- runs of each arm at the verdict load (any validity): the 'total' of 'valid of total'
lt AS (SELECT tier, campaign, test, workload, arm, count(*) AS n FROM lr WHERE load = getvariable('verdict_load') GROUP BY ALL),
-- isolation ------------------------------------------------------------------------------
iso_a AS (
  SELECT a.tier, a.campaign, a.test, a.workload, a.arm,
    coalesce(sum(i.checked), 0) AS checked, coalesce(sum(i.found), 0) AS found
  FROM arms a LEFT JOIN ok_runs r USING (tier, campaign, test, workload, arm)
  LEFT JOIN iso i ON i.dir = r.dir AND i.class IN ('state', 'canary', 'structural', 'identity')
  GROUP BY ALL),
v_iso AS (
  SELECT a.*, 'all' AS load, 'isolation' AS threshold, i.found::DOUBLE AS measured, '= 0' AS lim,
    CASE WHEN (SELECT sum(n_bad) FROM arm_bad b WHERE b.tier = a.tier AND b.campaign = a.campaign AND b.test = a.test
                 AND b.workload = a.workload AND b.arm = a.arm) > 0 THEN 'inconclusive'
         WHEN i.checked = 0 THEN 'inconclusive' WHEN i.found > 0 THEN 'fail' ELSE 'pass' END AS verdict,
    CASE WHEN (SELECT sum(n_bad) FROM arm_bad b WHERE b.tier = a.tier AND b.campaign = a.campaign AND b.test = a.test
                 AND b.workload = a.workload AND b.arm = a.arm) > 0 THEN 'a run of this arm is in table 0'
         WHEN i.checked = 0 THEN 'no oracle row checked' ELSE 'checked ' || i.checked END
      || coalesce('; invalid runs dropped ' || v.n_inv, '') AS note
  FROM arms a JOIN iso_a i USING (tier, campaign, test, workload, arm) LEFT JOIN arm_inv v USING (tier, campaign, test, workload, arm)),
-- per-run values at the verdict load -------------------------------------------------------
vr AS (
  SELECT o.tier, o.campaign, o.test, o.workload, o.arm, o.seed, o.rep, o.refused,
    o.goodput * 1e9 / o.duration_ns AS gp, q.q99 AS q99
  FROM ok_runs o LEFT JOIN run_q q ON q.dir = o.dir AND q.metric = 'lease_wait_ns'
  WHERE o.load = getvariable('verdict_load')),
grp AS (
  SELECT a.*, ba.base_arm, ba.aa_arm,
    (SELECT count(*) FROM vr WHERE vr.tier = a.tier AND vr.campaign = a.campaign AND vr.test = a.test AND vr.workload = a.workload AND vr.arm = a.arm) AS n_arm,
    (SELECT count(*) FROM vr WHERE vr.tier = a.tier AND vr.campaign = a.campaign AND vr.test = a.test AND vr.workload = a.workload AND vr.arm = ba.base_arm) AS n_base,
    (SELECT count(*) FROM vr WHERE vr.tier = a.tier AND vr.campaign = a.campaign AND vr.test = a.test AND vr.workload = a.workload AND vr.arm = ba.aa_arm) AS n_aa,
    (SELECT coalesce(sum(n_bad), 0) FROM arm_bad b WHERE b.tier = a.tier AND b.campaign = a.campaign AND b.test = a.test AND b.workload = a.workload
       AND b.load = getvariable('verdict_load') AND b.arm IN (a.arm, ba.base_arm, coalesce(ba.aa_arm, ''))) AS n_bad,
    (SELECT sum(refused) FROM vr WHERE vr.tier = a.tier AND vr.campaign = a.campaign AND vr.test = a.test AND vr.workload = a.workload AND vr.arm = a.arm) AS refused,
    (SELECT coalesce(sum(n), 0) FROM lt WHERE lt.tier = a.tier AND lt.campaign = a.campaign AND lt.test = a.test AND lt.workload = a.workload AND lt.arm = a.arm) AS t_arm,
    (SELECT coalesce(sum(n), 0) FROM lt WHERE lt.tier = a.tier AND lt.campaign = a.campaign AND lt.test = a.test AND lt.workload = a.workload AND lt.arm = ba.base_arm) AS t_base,
    (SELECT coalesce(sum(n), 0) FROM lt WHERE lt.tier = a.tier AND lt.campaign = a.campaign AND lt.test = a.test AND lt.workload = a.workload AND lt.arm = ba.aa_arm) AS t_aa
  FROM arms a LEFT JOIN base_arms ba ON ba.tier = a.tier AND ba.campaign = a.campaign AND ba.test = a.test
    AND ba.workload = a.workload AND ba.load = getvariable('verdict_load')),
reps AS (
  SELECT *, 'valid runs arm ' || n_arm || ' of ' || t_arm || ', baseline ' || n_base || ' of ' || t_base
      || CASE WHEN tier = 'real' THEN ', A/A ' || n_aa || ' of ' || t_aa ELSE '' END AS reps_note,
    n_arm < least(getvariable('min_valid_reps'), t_arm) OR n_base < least(getvariable('min_valid_reps'), t_base)
      OR (tier = 'real' AND n_aa < least(getvariable('min_valid_reps'), t_aa)) AS few_reps
  FROM grp),
missing AS (
  SELECT *, CASE WHEN n_bad > 0 THEN 'a run of the arm, the baseline or the A/A arm at this load is broken (table 0)'
                 WHEN t_arm = 0 THEN 'no run of the arm at this load'
                 WHEN base_arm IS NULL OR t_base = 0 THEN 'no baseline run at this load'
                 WHEN tier = 'real' AND (aa_arm IS NULL OR t_aa = 0) THEN 'no A/A arm at this load'
                 WHEN few_reps OR n_arm = 0 OR n_base = 0 OR (tier = 'real' AND n_aa = 0)
                   THEN 'too few valid runs (min ' || getvariable('min_valid_reps') || '): ' || reps_note END AS why
  FROM reps),
pairs AS (
  SELECT g.tier, g.campaign, g.test, g.workload, g.arm, x.gp / nullif(b.gp, 0) AS gp_ratio,
    x.q99.hi / nullif(b.q99.hi, 0) AS p99_ratio,
    x.q99.lo <= getvariable('p99_max_ratio') * b.q99.hi AND x.q99.hi > getvariable('p99_max_ratio') * b.q99.lo AS p99_straddles,
    b.q99.lo <= 1000 AS base_clamped, x.q99.hi AS arm_p99, b.q99.hi AS base_p99
  FROM grp g JOIN vr x ON x.tier = g.tier AND x.campaign = g.campaign AND x.test = g.test AND x.workload = g.workload AND x.arm = g.arm
  JOIN vr b ON b.tier = g.tier AND b.campaign = g.campaign AND b.test = g.test AND b.workload = g.workload AND b.arm = g.base_arm
    AND b.seed = x.seed AND b.rep = x.rep
  WHERE g.tier = 'sim'),
sim_a AS (
  SELECT tier, campaign, test, workload, arm, count(*) AS n_pairs, min(gp_ratio) AS gp_ratio,
    max(p99_ratio) FILTER (WHERE NOT base_clamped) AS p99_ratio,
    coalesce(bool_or(p99_straddles) FILTER (WHERE NOT base_clamped), false) AS p99_straddles,
    coalesce(bool_or(p99_ratio > getvariable('p99_max_ratio') AND NOT p99_straddles) FILTER (WHERE NOT base_clamped), false) AS p99_clear_fail,
    count(*) FILTER (WHERE base_clamped) AS n_clamped,
    max(arm_p99) FILTER (WHERE base_clamped) AS clamp_arm_p99, max(base_p99) FILTER (WHERE base_clamped) AS clamp_base_p99
  FROM pairs GROUP BY ALL),
med AS (
  SELECT tier, campaign, test, workload, arm, median(gp) AS gp, median(q99.hi)::DOUBLE AS p99, median(q99.lo)::DOUBLE AS p99_lo
  FROM vr WHERE tier = 'real' GROUP BY ALL),
real_a AS (
  SELECT g.tier, g.campaign, g.test, g.workload, g.arm, x.gp, b.gp AS base_gp, aa.gp AS aa_gp, x.p99, b.p99 AS base_p99, aa.p99 AS aa_p99,
    b.p99_lo <= 1000 AS base_clamped, abs(aa.gp - b.gp) AS gp_floor, abs(aa.p99 - b.p99) AS p99_floor
  FROM grp g JOIN med x ON x.tier = g.tier AND x.campaign = g.campaign AND x.test = g.test AND x.workload = g.workload AND x.arm = g.arm
  LEFT JOIN med b ON b.tier = g.tier AND b.campaign = g.campaign AND b.test = g.test AND b.workload = g.workload AND b.arm = g.base_arm
  LEFT JOIN med aa ON aa.tier = g.tier AND aa.campaign = g.campaign AND aa.test = g.test AND aa.workload = g.workload AND aa.arm = g.aa_arm
  WHERE g.tier = 'real'),
v_gp AS (
  SELECT m.tier, m.campaign, m.test, m.workload, m.policy, m.patience, m.warm_mode, m.arm, getvariable('verdict_load')::VARCHAR AS load,
    'goodput' AS threshold,
    CASE WHEN m.tier = 'sim' THEN s.gp_ratio ELSE r.gp / nullif(r.base_gp, 0) END AS measured,
    '>= ' || getvariable('goodput_min_ratio') || ' x baseline' || CASE WHEN m.tier = 'real' THEN '; not below by > 2 x A/A' ELSE '' END AS lim,
    CASE WHEN m.why IS NOT NULL THEN 'inconclusive'
         WHEN m.tier = 'sim' THEN CASE WHEN s.gp_ratio IS NULL THEN 'inconclusive' WHEN s.gp_ratio >= getvariable('goodput_min_ratio') THEN 'pass' ELSE 'fail' END
         WHEN r.base_gp IS NULL OR r.base_gp <= 0 OR r.gp_floor IS NULL THEN 'inconclusive'
         WHEN r.gp_floor >= (1 - getvariable('goodput_min_ratio')) * r.base_gp THEN 'inconclusive'
         WHEN r.gp / r.base_gp < getvariable('goodput_min_ratio') OR r.base_gp - r.gp > 2 * r.gp_floor THEN 'fail'
         ELSE 'pass' END AS verdict,
    coalesce(m.why, CASE WHEN m.tier = 'sim' THEN 'pairs ' || s.n_pairs || '; ' || m.reps_note
      ELSE 'ratio ' || round(r.gp / nullif(r.base_gp, 0), 6) || ' (min ' || getvariable('goodput_min_ratio') || '); d '
        || round(r.base_gp - r.gp, 3) || '/s against 2 x floor ' || round(2 * r.gp_floor, 3) || '/s'
        || CASE WHEN r.gp_floor >= (1 - getvariable('goodput_min_ratio')) * r.base_gp
                THEN ' (floor >= ' || round(100 * (1 - getvariable('goodput_min_ratio')), 3) || '% of the baseline)' ELSE '' END
        || '; arm ' || round(r.gp, 3) || '/s, baseline ' || round(r.base_gp, 3) || '/s, A/A ' || round(r.aa_gp, 3) || '/s; '
        || m.reps_note END) AS note
  FROM missing m LEFT JOIN sim_a s USING (tier, campaign, test, workload, arm) LEFT JOIN real_a r USING (tier, campaign, test, workload, arm)),
v_p99 AS (
  SELECT m.tier, m.campaign, m.test, m.workload, m.policy, m.patience, m.warm_mode, m.arm, getvariable('verdict_load')::VARCHAR AS load,
    'p99_wait' AS threshold,
    CASE WHEN m.tier = 'sim' THEN coalesce(s.p99_ratio, s.clamp_arm_p99)
         WHEN r.base_clamped THEN r.p99 ELSE r.p99 / nullif(r.base_p99, 0) END AS measured,
    CASE WHEN (m.tier = 'sim' AND s.p99_ratio IS NULL AND s.n_clamped > 0) OR (m.tier = 'real' AND r.base_clamped)
         THEN '<= ' || getvariable('p99_abs_floor_ns') || ' ns (absolute: baseline p99 <= 1 us)'
         ELSE '<= ' || getvariable('p99_max_ratio') || ' x baseline' END AS lim,
    CASE WHEN m.why IS NOT NULL THEN 'inconclusive'
         WHEN m.tier = 'sim' THEN CASE WHEN s.n_pairs IS NULL THEN 'inconclusive' WHEN s.p99_clear_fail THEN 'fail'
                                       WHEN s.p99_straddles THEN 'inconclusive'
                                       WHEN s.clamp_arm_p99 > getvariable('p99_abs_floor_ns') THEN 'inconclusive' ELSE 'pass' END
         WHEN r.base_p99 IS NULL OR r.p99_floor IS NULL THEN 'inconclusive'
         WHEN r.base_clamped THEN CASE WHEN r.p99 <= getvariable('p99_abs_floor_ns') THEN 'pass' ELSE 'inconclusive' END
         WHEN abs(r.p99 - getvariable('p99_max_ratio') * r.base_p99) <= 2 * r.p99_floor THEN 'inconclusive'
         WHEN r.p99 <= getvariable('p99_max_ratio') * r.base_p99 THEN 'pass' ELSE 'fail' END AS verdict,
    coalesce(m.why, '') || CASE WHEN m.why IS NULL THEN '' ELSE '; ' END || 'refused ' || coalesce(m.refused, 0)
      || CASE WHEN m.why IS NOT NULL THEN ''
              WHEN m.tier = 'sim' THEN
                CASE WHEN s.p99_straddles THEN '; bucket straddles the threshold' ELSE '' END
                || CASE WHEN s.n_clamped > 0 THEN '; baseline p99 below the histogram floor (<= 1 us) in ' || s.n_clamped
                     || ' of ' || s.n_pairs || ' pairs: arm p99 ' || s.clamp_arm_p99 || ' ns, baseline p99 ' || s.clamp_base_p99
                     || ' ns, bound ' || getvariable('p99_abs_floor_ns') || ' ns' ELSE '' END
                || '; ' || m.reps_note
              WHEN r.base_clamped THEN '; baseline p99 below the histogram floor (<= 1 us): arm p99 ' || r.p99::BIGINT
                || ' ns, baseline p99 ' || r.base_p99::BIGINT || ' ns, bound ' || getvariable('p99_abs_floor_ns') || ' ns; ' || m.reps_note
              ELSE '; arm p99 ' || r.p99::BIGINT || ' ns, ' || getvariable('p99_max_ratio') || ' x baseline '
                || (getvariable('p99_max_ratio') * r.base_p99)::BIGINT || ' ns, |difference| '
                || abs(r.p99 - getvariable('p99_max_ratio') * r.base_p99)::BIGINT || ' ns against 2 x floor '
                || (2 * r.p99_floor)::BIGINT || ' ns; A/A p99 ' || r.aa_p99::BIGINT || ' ns; ' || m.reps_note END AS note
  FROM missing m LEFT JOIN sim_a s USING (tier, campaign, test, workload, arm) LEFT JOIN real_a r USING (tier, campaign, test, workload, arm)),
-- warm stock (dynamic arms) ----------------------------------------------------------------
wm AS (
  SELECT a.tier, a.campaign, a.test, a.workload, a.arm, sum(w.misses) AS misses, sum(w.n) AS n, max(r.epsilon) AS eps
  FROM arms a JOIN ok_runs r USING (tier, campaign, test, workload, arm) JOIN run_miss w ON w.dir = r.dir
  WHERE a.warm_mode = 'dynamic' GROUP BY ALL),
v_miss AS (
  SELECT a.tier, a.campaign, a.test, a.workload, a.policy, a.patience, a.warm_mode, a.arm, 'all' AS load, 'warm_miss' AS threshold,
    w.misses / nullif(w.n, 0) AS measured, '<= epsilon ' || coalesce(w.eps::VARCHAR, '?') AS lim,
    CASE WHEN (SELECT sum(n_bad) FROM arm_bad b WHERE b.tier = a.tier AND b.campaign = a.campaign AND b.test = a.test AND b.workload = a.workload AND b.arm = a.arm) > 0
              OR w.n IS NULL OR w.n <= 0 OR w.eps IS NULL THEN 'inconclusive'
         WHEN abs(w.misses / w.n - w.eps) <= getvariable('band_z') * sqrt((w.misses / w.n) * (1 - w.misses / w.n) / w.n) THEN 'inconclusive'
         WHEN w.misses / w.n <= w.eps THEN 'pass' ELSE 'fail' END AS verdict,
    'misses ' || coalesce(w.misses, 0) || ' of ' || coalesce(w.n, 0) || coalesce('; invalid runs dropped ' || v.n_inv, '') AS note
  FROM arms a LEFT JOIN wm w USING (tier, campaign, test, workload, arm) LEFT JOIN arm_inv v USING (tier, campaign, test, workload, arm) WHERE a.warm_mode = 'dynamic'),
rr AS (
  SELECT o.tier, o.campaign, o.test, o.workload, o.load, o.seed, o.rep, o.policy, o.patience_label AS patience, o.warm_mode, o.arm,
    w.w, w.misses / nullif(w.n, 0) AS miss_rate, w.warm_conn_s, o.epsilon
  FROM run_miss w JOIN ok_runs o USING (dir) WHERE o.warm_mode IN ('fixed', 'dynamic')),
hs AS (
  SELECT d.tier, d.campaign, d.test, d.workload, d.arm, d.load, d.seed, d.rep, d.warm_conn_s,
    (SELECT arg_min(f.warm_conn_s, f.w) FROM rr f WHERE f.warm_mode = 'fixed' AND f.tier = d.tier AND f.campaign = d.campaign
       AND f.test = d.test AND f.workload = d.workload AND f.load = d.load AND f.seed = d.seed AND f.rep = d.rep
       AND f.policy = d.policy AND f.patience = d.patience AND f.miss_rate <= d.epsilon) AS hind_wcs
  FROM rr d WHERE d.warm_mode = 'dynamic'),
v_wcs AS (
  SELECT a.tier, a.campaign, a.test, a.workload, a.policy, a.patience, a.warm_mode, a.arm, 'all' AS load, 'warm_conn_s' AS threshold,
    max(h.warm_conn_s / nullif(h.hind_wcs, 0)) AS measured, '<= ' || getvariable('wcs_max_ratio') || ' x hindsight-fixed' AS lim,
    CASE WHEN count(h.arm) = 0 OR count(h.hind_wcs) < count(h.arm) OR count(h.hind_wcs) FILTER (WHERE h.hind_wcs > 0) < count(h.arm) THEN 'inconclusive'
         WHEN max(h.warm_conn_s / h.hind_wcs) <= getvariable('wcs_max_ratio') THEN 'pass' ELSE 'fail' END AS verdict,
    'comparisons ' || count(h.arm) || ', with a hindsight W ' || count(h.hind_wcs) AS note
  FROM arms a LEFT JOIN hs h USING (tier, campaign, test, workload, arm) WHERE a.warm_mode = 'dynamic' GROUP BY ALL),
allv AS (
  SELECT tier, campaign, test, workload, policy, patience, warm_mode, arm, load, threshold, measured, lim, verdict, note FROM v_iso
  UNION ALL SELECT * FROM v_gp
  UNION ALL SELECT * FROM v_p99
  UNION ALL SELECT * FROM v_miss
  UNION ALL SELECT * FROM v_wcs)
SELECT tier, campaign, test, workload, policy, patience, warm_mode, arm, load, threshold, round(measured, 6) AS measured,
  lim AS threshold_value, verdict, note
FROM allv
ORDER BY tier, campaign, test, workload, policy, patience, arm,
  CASE threshold WHEN 'isolation' THEN 1 WHEN 'goodput' THEN 2 WHEN 'p99_wait' THEN 3 WHEN 'warm_miss' THEN 4 ELSE 5 END;
