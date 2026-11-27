-- Lease-lab report: the shared views. scripts/lease-report.sh runs params.sql + this file once
-- into the report database (<out-dir>/report.duckdb); every table file then reads its tables.
-- DuckDB 1.5.
--
-- Reads, under the campaign directory `root`, every run directory at any depth: the flushed
-- files of schema version 1 (metrics.csv, histograms.csv, run.csv) and the sidecars of
-- NOTES-E2.md section 3 (lab.csv, oracle.csv, leases.csv) plus E5's server.csv and cpu.csv.
-- A run directory is any directory holding one of those files. Its identity is its path
-- (`dir`), not run.csv's run_id, so two campaigns may reuse a run id.
-- Every glob list starts with a header-only file from `sentinel`, so an empty campaign reads
-- as zero rows instead of failing; the sentinel directory itself is never a run.
--
-- Parameters: params.sql (read before this file).

-- Files -----------------------------------------------------------------------------------
CREATE OR REPLACE MACRO lr_files(name) AS (
  SELECT coalesce(list(file ORDER BY file), []) FROM glob(getvariable('root') || '/**/' || name));
SET VARIABLE f_metrics = lr_files('metrics.csv');
SET VARIABLE f_hist = lr_files('histograms.csv');
SET VARIABLE f_run = lr_files('run.csv');
SET VARIABLE f_lab = lr_files('lab.csv');
SET VARIABLE f_oracle = lr_files('oracle.csv');
SET VARIABLE f_leases = lr_files('leases.csv');
SET VARIABLE f_server = lr_files('server.csv');
SET VARIABLE f_cpu = lr_files('cpu.csv');
CREATE OR REPLACE MACRO lr_dir(f) AS regexp_replace(f, '/[^/]*$', '');
CREATE OR REPLACE MACRO lr_with_sentinel(l, name) AS list_prepend(getvariable('sentinel') || '/' || name, l);

CREATE OR REPLACE TABLE run_files AS
SELECT lr_dir(f) AS dir, regexp_extract(f, '[^/]*$') AS file
FROM (SELECT unnest(getvariable('f_metrics') || getvariable('f_hist') || getvariable('f_run') || getvariable('f_lab')
              || getvariable('f_oracle') || getvariable('f_leases') || getvariable('f_server') || getvariable('f_cpu')) AS f);

-- Raw readers ------------------------------------------------------------------------------
CREATE OR REPLACE TABLE m AS
SELECT lr_dir(filename) AS dir, flush_seq, t_ns, coalesce(component, '') AS component, coalesce(metric, '') AS metric,
       coalesce(outcome, '') AS outcome, coalesce(reason, '') AS reason, coalesce(mode, '') AS mode, value
FROM read_csv(lr_with_sentinel(getvariable('f_metrics'), 'metrics.csv'), header = true, filename = true,
  columns = {'schema_version': 'INTEGER', 'run_id': 'VARCHAR', 'flush_seq': 'BIGINT', 't_ns': 'BIGINT',
             'instance': 'VARCHAR', 'component': 'VARCHAR', 'metric': 'VARCHAR', 'kind': 'VARCHAR',
             'tenant': 'VARCHAR', 'outcome': 'VARCHAR', 'reason': 'VARCHAR', 'mode': 'VARCHAR', 'value': 'BIGINT'})
WHERE component = 'pool';

CREATE OR REPLACE TABLE last_flush AS SELECT dir, max(flush_seq) AS seq FROM m GROUP BY dir;

CREATE OR REPLACE TABLE fm AS
SELECT m.* FROM m JOIN last_flush l ON m.dir = l.dir AND m.flush_seq = l.seq;

-- Histograms: the last flush only (counts are cumulative).
CREATE OR REPLACE TABLE fh AS
SELECT lr_dir(filename) AS dir, coalesce(metric, '') AS metric, coalesce(outcome, '') AS outcome,
       coalesce(mode, '') AS mode, bucket_lo AS lo, bucket_hi AS hi, count AS c
FROM read_csv(lr_with_sentinel(getvariable('f_hist'), 'histograms.csv'), header = true, filename = true,
  columns = {'schema_version': 'INTEGER', 'run_id': 'VARCHAR', 'flush_seq': 'BIGINT', 't_ns': 'BIGINT',
             'instance': 'VARCHAR', 'component': 'VARCHAR', 'metric': 'VARCHAR',
             'tenant': 'VARCHAR', 'outcome': 'VARCHAR', 'reason': 'VARCHAR', 'mode': 'VARCHAR',
             'unit': 'VARCHAR', 'bucket_lo': 'BIGINT', 'bucket_hi': 'BIGINT', 'count': 'BIGINT'}) h
SEMI JOIN last_flush l ON lr_dir(h.filename) = l.dir AND h.flush_seq = l.seq
WHERE component = 'pool';

CREATE OR REPLACE TABLE runcsv AS
SELECT lr_dir(filename) AS dir, run_id, tier AS run_tier, seed AS run_seed, arm AS run_arm, duration_ns AS run_duration_ns
FROM read_csv(lr_with_sentinel(getvariable('f_run'), 'run.csv'), header = true, filename = true,
  columns = {'schema_version': 'INTEGER', 'run_id': 'VARCHAR', 'scenario': 'VARCHAR', 'tier': 'VARCHAR',
             'seed': 'BIGINT', 'arm': 'VARCHAR', 'instances': 'BIGINT', 'build': 'VARCHAR',
             'config_fingerprint': 'VARCHAR', 'flushes': 'BIGINT', 'duration_ns': 'BIGINT'});

CREATE OR REPLACE TABLE lab AS
SELECT lr_dir(filename) AS dir, key, coalesce(value, '') AS value
FROM read_csv(lr_with_sentinel(getvariable('f_lab'), 'lab.csv'), header = true, filename = true,
  columns = {'key': 'VARCHAR', 'value': 'VARCHAR'});

CREATE OR REPLACE TABLE oracle AS
SELECT lr_dir(filename) AS dir, * EXCLUDE (filename)
FROM read_csv(lr_with_sentinel(getvariable('f_oracle'), 'oracle.csv'), header = true, filename = true,
  columns = {'lease': 'BIGINT', 'tenant': 'BIGINT', 'prev_tenant': 'BIGINT', 'conn': 'BIGINT', 'backend_pid': 'BIGINT',
             'backend_start_ns': 'BIGINT', 'prev_taint': 'VARCHAR', 'taint': 'VARCHAR', 'prepared': 'BIGINT',
             'cursors': 'BIGINT', 'listens': 'BIGINT', 'advisory_locks': 'BIGINT', 'settings_changed': 'BIGINT',
             'temp_relations': 'BIGINT', 'role_changed': 'BIGINT', 'reset_between': 'BIGINT', 'canary_kind': 'VARCHAR',
             'canary_read': 'BIGINT', 'probe_seed_inherited': 'BIGINT', 'probe_custom_name': 'BIGINT', 'note': 'VARCHAR'});

CREATE OR REPLACE TABLE leases AS
SELECT lr_dir(filename) AS dir, * EXCLUDE (filename)
FROM read_csv(lr_with_sentinel(getvariable('f_leases'), 'leases.csv'), header = true, filename = true,
  columns = {'lease': 'BIGINT', 'tenant': 'BIGINT', 'intended_ns': 'BIGINT', 'granted_ns': 'BIGINT',
             'released_ns': 'BIGINT', 'outcome': 'VARCHAR', 'switch': 'VARCHAR', 'conn': 'BIGINT',
             'prev_tenant': 'BIGINT', 'status': 'VARCHAR'});

CREATE OR REPLACE TABLE server AS
SELECT lr_dir(filename) AS dir, TRY_CAST("time" AS VARCHAR) AS t, TRY_CAST("count" AS BIGINT) AS n
FROM read_csv(lr_with_sentinel(getvariable('f_server'), 'server.csv'), header = true, filename = true,
  union_by_name = true, all_varchar = true);

CREATE OR REPLACE TABLE cpu AS
SELECT lr_dir(filename) AS dir, sum(TRY_CAST(pg_cpu_ns AS BIGINT)) AS pg_cpu_ns, sum(TRY_CAST(lab_cpu_ns AS BIGINT)) AS lab_cpu_ns
FROM read_csv(lr_with_sentinel(getvariable('f_cpu'), 'cpu.csv'), header = true, filename = true,
  union_by_name = true, all_varchar = true)
GROUP BY ALL;

-- Calibration ------------------------------------------------------------------------------
CREATE OR REPLACE TABLE cal_costs AS
SELECT c.measurement, c.path, c.cond, c.part, c.field, c.unit, c.stats.n AS n, c.stats.p50 AS p50,
       c.stats.p90 AS p90, c.stats.p99 AS p99, c.failed
FROM (SELECT unnest(costs) AS c FROM read_json(getvariable('calibration'), maximum_object_size = 268435456));
CREATE OR REPLACE TABLE cal_rates AS
SELECT r.measurement, r.path, r.cond, r.rate_milli_per_s.p50 AS p50_milli,
       r.rate_milli_per_s_uncontended.p50 AS p50_uncontended_milli, r.rate_milli_per_s_uncontended.n AS n_uncontended
FROM (SELECT unnest(rates) AS r FROM read_json(getvariable('calibration'), maximum_object_size = 268435456));
CREATE OR REPLACE TABLE cal_cells AS
SELECT c.measurement, c.path, c.cond, c.excluded, c.contended, c.pg_cpu_ns_per_connect, c.probe_cpu_ns_per_connect
FROM (SELECT unnest(cells) AS c FROM read_json(getvariable('calibration'), maximum_object_size = 268435456));
CREATE OR REPLACE TABLE cal_id AS
SELECT calibration_id FROM read_json(getvariable('calibration'), maximum_object_size = 268435456);
-- The few calibrated numbers the tables use (bridge path, p50, ns):
CREATE OR REPLACE TABLE cal AS SELECT
  (SELECT p50 FROM cal_costs WHERE measurement = 'M2I' AND path = 'bridge' AND cond = 'none' AND part = 'discard' AND field = 'ns') AS reset_ns,
  (SELECT p50 FROM cal_costs WHERE measurement = 'M3' AND path = 'bridge' AND cond = 'c=1' AND field = 'ns') AS connect_ns,
  (SELECT p50 FROM cal_costs WHERE measurement = 'M4' AND path = 'bridge' AND cond = 'poll' AND part = 'close_to_gone' AND field = 'ns') AS close_ns,
  (SELECT p50_uncontended_milli FROM cal_rates WHERE measurement = 'M7S' AND path = 'bridge' AND cond = getvariable('ceiling_cond')) AS ceiling_milli,
  (SELECT median(pg_cpu_ns_per_connect) FROM cal_cells WHERE measurement = 'M7S' AND path = 'bridge' AND NOT excluded AND NOT contended) AS pg_cpu_per_connect_ns,
  (SELECT median(probe_cpu_ns_per_connect) FROM cal_cells WHERE measurement = 'M7S' AND path = 'bridge' AND NOT excluded AND NOT contended) AS client_cpu_per_connect_ns;

-- Runs: one row per run directory, with its labels from lab.csv ------------------------------
CREATE OR REPLACE MACRO lr_dur_ns(s) AS (  -- '1s', '21ms', '500us', '43829ns' -> ns
  CASE WHEN s IS NULL OR s = '' THEN NULL
       WHEN s LIKE '%ns' THEN TRY_CAST(left(s, -2) AS DOUBLE)
       WHEN s LIKE '%us' THEN TRY_CAST(left(s, -2) AS DOUBLE) * 1e3
       WHEN s LIKE '%µs' THEN TRY_CAST(left(s, -2) AS DOUBLE) * 1e3
       WHEN s LIKE '%ms' THEN TRY_CAST(left(s, -2) AS DOUBLE) * 1e6
       WHEN s LIKE '%s' THEN TRY_CAST(left(s, -1) AS DOUBLE) * 1e9
       WHEN s LIKE '%m' THEN TRY_CAST(left(s, -1) AS DOUBLE) * 6e10 END);
CREATE OR REPLACE MACRO lr_frac(s) AS (  -- '1%' -> 0.01, '0.1%' -> 0.001, '0.02' -> 0.02
  CASE WHEN s LIKE '%\%%' ESCAPE '\' THEN TRY_CAST(left(s, -1) AS DOUBLE) / 100 ELSE TRY_CAST(s AS DOUBLE) END);

CREATE OR REPLACE TABLE runs AS
WITH d AS (SELECT DISTINCT dir FROM run_files),
k AS (
  SELECT dir,
    max(value) FILTER (WHERE key = 'sidecar.version') AS sidecar_version,
    max(value) FILTER (WHERE key = 'tier') AS tier,
    max(value) FILTER (WHERE key = 'calibration.id') AS calibration_id,
    coalesce(max(value) FILTER (WHERE key = 'run.campaign'), '') AS campaign,
    coalesce(max(value) FILTER (WHERE key = 'run.test'), '') AS test,
    max(value) FILTER (WHERE key = 'run.arm') AS arm_label,
    coalesce(max(value) FILTER (WHERE key = 'run.rep'), '') AS rep,
    max(value) FILTER (WHERE key = 'release.policy') AS policy,
    max(value) FILTER (WHERE key = 'warm.mode') AS warm_mode,
    TRY_CAST(max(value) FILTER (WHERE key = 'warm.size') AS BIGINT) AS warm_size,
    max(value) FILTER (WHERE key = 'warm.epsilon') AS epsilon_text,
    coalesce(max(value) FILTER (WHERE key = 'warm.prime'), '') AS prime,
    max(value) FILTER (WHERE key = 'remake.patience') AS patience,
    coalesce(max(value) FILTER (WHERE key = 'remake.order'), '') AS remake_order,
    coalesce(max(value) FILTER (WHERE key = 'reset.reseed'), '') AS reseed,
    max(value) FILTER (WHERE key = 'affinity') AS affinity,
    TRY_CAST(max(value) FILTER (WHERE key = 'pool.cap') AS BIGINT) AS cap,
    TRY_CAST(max(value) FILTER (WHERE key = 'connect.max_concurrent') AS BIGINT) AS max_concurrent,
    max(value) FILTER (WHERE key = 'workload.profile') AS profile,
    max(value) FILTER (WHERE key = 'workload.tenants') AS tenants,
    max(value) FILTER (WHERE key = 'workload.skew') AS skew,
    max(value) FILTER (WHERE key = 'workload.lease') AS lease_dist,
    max(value) FILTER (WHERE key = 'workload.stmt_class') AS stmt_class,
    max(value) FILTER (WHERE key = 'workload.script') AS script,
    TRY_CAST(max(value) FILTER (WHERE key = 'workload.load') AS DOUBLE) AS load,
    coalesce(max(value) FILTER (WHERE key = 'seed'), '') AS seed,
    TRY_CAST(max(value) FILTER (WHERE key = 'run.duration_ns') AS BIGINT) AS duration_ns,
    TRY_CAST(max(value) FILTER (WHERE key = 'run.end_ns') AS BIGINT) AS end_ns,
    TRY_CAST(max(value) FILTER (WHERE key = 'requests.intended_milli') AS BIGINT) AS intended_milli,
    TRY_CAST(max(value) FILTER (WHERE key = 'requests.achieved') AS BIGINT) AS achieved,
    max(value) FILTER (WHERE key = 'run.result') AS result,
    max(value) FILTER (WHERE key = getvariable('invalid_key')) AS runner_valid,
    TRY_CAST(max(value) FILTER (WHERE key = 'oracle.rate') AS DOUBLE) AS oracle_rate,
    max(value) FILTER (WHERE key = 'obs.leases_csv') AS leases_csv,
    TRY_CAST(max(value) FILTER (WHERE key = 'cm.fit.cores') AS DOUBLE) AS cm_cores,
    TRY_CAST(max(value) FILTER (WHERE key = 'cm.fit.work_ns') AS DOUBLE) AS cm_work_ns,
    TRY_CAST(max(value) FILTER (WHERE key = 'cm.fit.close_work_ns') AS DOUBLE) AS cm_close_work_ns,
    max(value) FILTER (WHERE key = 'cm.cores') AS cm_cores_run,
    max(value) FILTER (WHERE key = 'cm.work') AS cm_work_run,
    max(value) FILTER (WHERE key = 'cm.close_work') AS cm_close_work_run,
    max(value) FILTER (WHERE key = 'cm.avail_pm') AS cm_avail_pm,
    TRY_CAST(max(value) FILTER (WHERE key = getvariable('cpu_pg_key')) AS BIGINT) AS lab_pg_cpu_ns,
    TRY_CAST(max(value) FILTER (WHERE key = getvariable('cpu_lab_key')) AS BIGINT) AS lab_lab_cpu_ns
  FROM lab GROUP BY dir)
SELECT d.dir, regexp_extract(d.dir, '[^/]*$') AS run_id, k.* EXCLUDE (dir),
  coalesce(nullif(k.arm_label, ''), k.policy || '+' || k.warm_mode) AS arm,
  CASE WHEN k.policy = 'remake-on-switch' THEN coalesce(k.patience, 'auto') ELSE '' END AS patience_label,
  lr_frac(k.epsilon_text) AS epsilon,
  -- the workload a comparison holds fixed (load is a separate key)
  concat_ws('|', k.profile, 'tenants=' || k.tenants, 'skew=' || k.skew, k.lease_dist, k.stmt_class, k.script,
            'cap=' || k.cap, 'dur=' || k.duration_ns) AS workload,
  lr_dur_ns(k.cm_work_run) AS cm_work_run_ns, lr_dur_ns(k.cm_close_work_run) AS cm_close_work_run_ns,
  TRY_CAST(k.cm_cores_run AS DOUBLE) AS cm_cores_run_n,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'metrics.csv') AS has_metrics,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'histograms.csv') AS has_hist,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'run.csv') AS has_runcsv,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'lab.csv') AS has_lab,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'oracle.csv') AS has_oracle,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'leases.csv') AS has_leases,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'server.csv') AS has_server,
  EXISTS (SELECT 1 FROM run_files f WHERE f.dir = d.dir AND f.file = 'cpu.csv') AS has_cpu
FROM d LEFT JOIN k ON d.dir = k.dir;

-- Pool counters and gauges at the last flush, one row per run ---------------------------------
CREATE OR REPLACE TABLE pm AS
SELECT dir,
  sum(value) FILTER (WHERE metric = 'leases_requested') AS requested,
  sum(value) FILTER (WHERE metric = 'leases_refused') AS refused,
  sum(value) FILTER (WHERE metric = 'leases_goodput') AS goodput,
  sum(value) FILTER (WHERE metric = 'leases_granted') AS granted,
  sum(value) FILTER (WHERE metric = 'leases_granted' AND outcome = 'reuse') AS g_reuse,
  sum(value) FILTER (WHERE metric = 'leases_granted' AND outcome = 'warm_hit') AS g_warm_hit,
  sum(value) FILTER (WHERE metric = 'leases_granted' AND outcome = 'miss_connect') AS g_miss_connect,
  sum(value) FILTER (WHERE metric = 'leases_granted' AND outcome = 'miss_slot') AS g_miss_slot,
  sum(value) FILTER (WHERE metric = 'leases_released') AS released,
  sum(value) FILTER (WHERE metric = 'leases_switch' AND mode IN ('warm', 'connect', 'reset', 'ready')) AS switches,
  sum(value) FILTER (WHERE metric = 'leases_switch' AND mode = 'none') AS no_switch,
  sum(value) FILTER (WHERE metric = 'switch_arrivals') AS switch_arrivals,
  sum(value) FILTER (WHERE metric = 'stock_empty_arrivals') AS stock_empty_arrivals,
  sum(value) FILTER (WHERE metric = 'stock_empty_free_slot') AS stock_empty_free_slot,
  sum(value) FILTER (WHERE metric = 'patience_waits') AS patience_waits,
  sum(value) FILTER (WHERE metric = 'patience_expired') AS patience_expired,
  sum(value) FILTER (WHERE metric = 'connects' AND outcome = 'started') AS connects_started,
  sum(value) FILTER (WHERE metric = 'connects' AND outcome = 'ok') AS connects_ok,
  sum(value) FILTER (WHERE metric = 'closes') AS closes,
  sum(value) FILTER (WHERE metric = 'closes_done') AS closes_done,
  sum(value) FILTER (WHERE metric = 'resets' AND outcome = 'ok') AS resets_ok,
  sum(value) FILTER (WHERE metric = 'db_lost') AS db_lost,
  sum(value) FILTER (WHERE metric = 'waiters') AS waiters,
  sum(value) FILTER (WHERE metric = 'pending') AS pending,
  sum(value) FILTER (WHERE metric = 'lent') AS lent,
  sum(value) FILTER (WHERE metric = 'open') AS open,
  sum(value) FILTER (WHERE metric = 'lent_ns_total') AS lent_ns,
  sum(value) FILTER (WHERE metric = 'warm_ns_total') AS warm_ns,
  sum(value) FILTER (WHERE metric = 'connecting_ns_total') AS connecting_ns,
  sum(value) FILTER (WHERE metric = 'conn_ns_total') AS conn_ns,
  max(value) FILTER (WHERE metric = 'open_connecting_hw') AS open_connecting_hw,
  max(value) FILTER (WHERE metric = 'db_backends_hw') AS db_backends_hw,
  sum(value) FILTER (WHERE metric = 'oracle_checks' AND outcome = 'dirty') AS oracle_dirty,
  sum(value) FILTER (WHERE metric = 'canary_reads' AND outcome = 'leak') AS canary_leaks
FROM fm GROUP BY dir;

-- Problems: everything that makes a run unreadable (severity 'broken') or excluded by its
-- runner (severity 'invalid'). Table 00 prints it; the other tables use `ok_runs` only.
CREATE OR REPLACE TABLE problems AS
WITH p AS (
  SELECT dir, flush_seq,
    sum(value) FILTER (WHERE metric = 'leases_requested') AS requested,
    sum(value) FILTER (WHERE metric = 'leases_granted') AS granted,
    sum(value) FILTER (WHERE metric = 'leases_released') AS released,
    sum(value) FILTER (WHERE metric = 'leases_refused') AS refused,
    sum(value) FILTER (WHERE metric = 'waiters') AS waiters,
    sum(value) FILTER (WHERE metric = 'pending') AS pending,
    sum(value) FILTER (WHERE metric = 'lent') AS lent,
    sum(value) FILTER (WHERE metric = 'connects' AND outcome = 'ok') AS connects_ok,
    sum(value) FILTER (WHERE metric = 'closes_done') AS closes_done,
    sum(value) FILTER (WHERE metric = 'open') AS open,
    sum(value) FILTER (WHERE metric IN ('lent_ns_total', 'warm_ns_total', 'connecting_ns_total', 'priming_ns_total',
                                       'resetting_ns_total', 'closing_ns_total', 'idle_ns_total')) AS states_ns,
    sum(value) FILTER (WHERE metric = 'conn_ns_total') AS conn_ns
  FROM m GROUP BY dir, flush_seq)
SELECT 'broken' AS severity, '(campaign)' AS dir, 'no run directory' AS "check",
       'no run directory under ' || getvariable('root') AS detail
WHERE NOT EXISTS (SELECT 1 FROM runs)
UNION ALL SELECT 'broken', dir, 'missing file', 'metrics.csv' FROM runs WHERE NOT has_metrics
UNION ALL SELECT 'broken', dir, 'missing file', 'histograms.csv' FROM runs WHERE NOT has_hist
UNION ALL SELECT 'broken', dir, 'missing file', 'run.csv (the run did not finish)' FROM runs WHERE NOT has_runcsv
UNION ALL SELECT 'broken', dir, 'missing file', 'lab.csv' FROM runs WHERE NOT has_lab
UNION ALL SELECT 'broken', dir, 'missing file', 'oracle.csv' FROM runs WHERE has_lab AND NOT has_oracle
UNION ALL SELECT 'broken', dir, 'missing file', 'leases.csv (obs.leases_csv=on)' FROM runs WHERE leases_csv = 'on' AND NOT has_leases
UNION ALL SELECT 'broken', dir, 'missing file', 'server.csv (real tier, remake policy)' FROM runs
  WHERE tier = 'real' AND policy LIKE 'remake-%' AND NOT has_server
UNION ALL SELECT 'broken', dir, 'bad lab.csv', 'sidecar.version ' || coalesce(sidecar_version, '(none)') || ', expected 1'
  FROM runs WHERE has_lab AND sidecar_version IS DISTINCT FROM '1'
UNION ALL SELECT 'broken', dir, 'bad lab.csv', 'missing or unknown ' || k FROM runs,
  (VALUES ('tier'), ('release.policy'), ('warm.mode'), ('workload.load'), ('run.duration_ns'), ('pool.cap')) v(k)
  WHERE has_lab AND CASE k WHEN 'tier' THEN tier NOT IN ('sim', 'real') OR tier IS NULL
                            WHEN 'release.policy' THEN policy IS NULL
                            WHEN 'warm.mode' THEN warm_mode IS NULL
                            WHEN 'workload.load' THEN load IS NULL
                            WHEN 'run.duration_ns' THEN duration_ns IS NULL OR duration_ns <= 0
                            WHEN 'pool.cap' THEN cap IS NULL END
UNION ALL SELECT 'broken', r.dir, 'no lease', 'leases_requested is 0 or absent in the last flush'
  FROM runs r LEFT JOIN pm USING (dir) WHERE r.has_metrics AND coalesce(pm.requested, 0) = 0
UNION ALL SELECT 'broken', r.dir, 'requests', 'lab.csv requests.achieved ' || r.achieved || ' <> leases_requested ' || pm.requested
  FROM runs r JOIN pm USING (dir) WHERE r.achieved IS DISTINCT FROM pm.requested AND r.has_lab
UNION ALL SELECT 'broken', dir, 'conservation', 'flush ' || flush_seq || ': requested ' || requested || ' <> granted ' || granted
       || ' + waiting ' || waiters || ' + pending ' || pending || ' + refused ' || refused
  FROM p WHERE requested IS DISTINCT FROM granted + waiters + pending + refused
UNION ALL SELECT 'broken', dir, 'conservation', 'flush ' || flush_seq || ': granted ' || granted || ' <> released ' || released || ' + lent ' || lent
  FROM p WHERE granted IS DISTINCT FROM released + lent
UNION ALL SELECT 'broken', dir, 'conservation', 'flush ' || flush_seq || ': connects ok ' || connects_ok || ' <> closes done ' || closes_done || ' + open ' || open
  FROM p WHERE connects_ok IS DISTINCT FROM closes_done + open
UNION ALL SELECT 'broken', dir, 'conservation', 'flush ' || flush_seq || ': time in states ' || states_ns || ' <> connection time ' || conn_ns
  FROM p WHERE states_ns IS DISTINCT FROM conn_ns
UNION ALL SELECT 'broken', r.dir, 'cap', 'open+connecting high-water ' || pm.open_connecting_hw || ' > pool.cap ' || r.cap
  FROM runs r JOIN pm USING (dir) WHERE pm.open_connecting_hw > r.cap
UNION ALL SELECT 'broken', r.dir, 'server bound', 'db_backends high-water ' || pm.db_backends_hw || ' > bound '
       || (r.cap + CASE WHEN r.remake_order = 'open-then-close' THEN r.max_concurrent ELSE 0 END + getvariable('server_extra'))
  FROM runs r JOIN pm USING (dir)
  WHERE pm.db_backends_hw > r.cap + CASE WHEN r.remake_order = 'open-then-close' THEN r.max_concurrent ELSE 0 END + getvariable('server_extra')
UNION ALL SELECT 'broken', r.dir, 'server bound', 'server.csv max count ' || s.mx || ' > bound '
       || (r.cap + CASE WHEN r.remake_order = 'open-then-close' THEN r.max_concurrent ELSE 0 END + getvariable('server_extra'))
  FROM runs r JOIN (SELECT dir, max(n) AS mx FROM server GROUP BY dir) s USING (dir)
  WHERE s.mx > r.cap + CASE WHEN r.remake_order = 'open-then-close' THEN r.max_concurrent ELSE 0 END + getvariable('server_extra')
UNION ALL SELECT 'broken', dir, 'server.csv', count(*) || ' rows without an integer count column' FROM server WHERE n IS NULL GROUP BY dir
UNION ALL SELECT 'broken', dir, 'server.csv', 'no rows' FROM runs WHERE has_server AND dir NOT IN (SELECT dir FROM server)
UNION ALL SELECT 'broken', min(dir), 'duplicate labels', count(*) || ' runs share campaign/test/arm/load/seed/rep '
       || concat_ws('/', campaign, test, arm, load, seed, rep)
  FROM runs WHERE has_lab GROUP BY tier, campaign, test, arm, load, seed, rep HAVING count(*) > 1
UNION ALL SELECT 'invalid', dir, 'runner', 'run.result ' || coalesce(result, '(none)') FROM runs WHERE has_lab AND result IS DISTINCT FROM 'ok'
UNION ALL SELECT 'invalid', dir, 'runner', getvariable('invalid_key') || ' ' || runner_valid FROM runs
  WHERE runner_valid IS NOT NULL AND NOT list_contains(getvariable('invalid_ok'), lower(runner_valid))
UNION ALL SELECT 'invalid', r.dir, 'db lost', 'real-tier run with db_lost ' || pm.db_lost FROM runs r JOIN pm USING (dir)
  WHERE r.tier = 'real' AND pm.db_lost > 0;

-- The runs every other table reads: complete, conserved and not flagged.
CREATE OR REPLACE TABLE ok_runs AS
SELECT r.*, pm.* EXCLUDE (dir) FROM runs r JOIN pm USING (dir)
WHERE r.dir NOT IN (SELECT dir FROM problems);

-- Comparison groups: same tier, campaign, test, workload and load. The baseline arm is
-- none + cold; the A/A partner is the second such arm in the group.
CREATE OR REPLACE TABLE base_arms AS
WITH a AS (SELECT DISTINCT tier, campaign, test, workload, load, arm FROM runs
           WHERE policy = 'none' AND warm_mode = 'cold' AND has_lab)
SELECT tier, campaign, test, workload, load,
  CASE WHEN getvariable('baseline_arm') <> '' THEN getvariable('baseline_arm') ELSE list_sort(list(arm))[1] END AS base_arm,
  CASE WHEN getvariable('aa_arm') <> '' THEN getvariable('aa_arm') ELSE list_sort(list(arm))[2] END AS aa_arm
FROM a GROUP BY ALL;

-- Histogram quantile: b = list of {lo, hi, c} sorted by lo (merged buckets); returns the
-- bucket {lo, hi} holding the ceil(q * n)-th value. Values are reported as the bucket's upper
-- edge `hi`; the bucket's relative width (hi - lo) / lo is the resolution error.
CREATE OR REPLACE MACRO lr_hq(b, q) AS (
  list_filter(
    list_transform(range(1, len(b) + 1), lambda i: {'lo': b[i].lo, 'hi': b[i].hi,
      'cum': list_sum(list_transform(list_slice(b, 1, i), lambda x: x.c))}),
    lambda s: s.cum >= ceil(q * list_sum(list_transform(b, lambda x: x.c))))[1]);
CREATE OR REPLACE MACRO lr_res_ppm(bk) AS (CASE WHEN bk.lo > 0 THEN (bk.hi - bk.lo) * 1000000 // bk.lo END);

-- Per-run wait quantiles: lease_wait_ns over every outcome; switch_ready_ns over the switch
-- modes (warm, connect, reset, ready). Refused leases are in no histogram.
CREATE OR REPLACE TABLE run_q AS
WITH b AS (
  SELECT dir, 'lease_wait_ns' AS metric, lo, hi, sum(c) AS c FROM fh WHERE metric = 'lease_wait_ns' GROUP BY ALL
  UNION ALL
  SELECT dir, 'switch_ready_ns', lo, hi, sum(c) FROM fh WHERE metric = 'switch_ready_ns' AND mode IN ('warm', 'connect', 'reset', 'ready') GROUP BY ALL),
l AS (SELECT dir, metric, list({'lo': lo, 'hi': hi, 'c': c} ORDER BY lo) AS b, sum(c) AS n FROM b GROUP BY dir, metric)
SELECT dir, metric, n, lr_hq(b, 0.5) AS q50, lr_hq(b, 0.9) AS q90, lr_hq(b, 0.99) AS q99, lr_hq(b, 0.999) AS q999 FROM l;

-- Warm-stock model miss, per run (REVIEW-E2-2 finding 2): arrivals that found the stock
-- empty with NO slot free are outside the Poisson model and leave the denominator.
--   misses = stock_empty_free_slot
--   n      = switch_arrivals - (stock_empty_arrivals - stock_empty_free_slot)
--   L      = connecting_ns_total / connects{started}   (measured mean connect time)
--   lambda = n / run.duration                          (measured fresh-connection arrival rate)
--   m      = lambda * L;  W = warm.size (fixed), 0 (cold)
CREATE OR REPLACE TABLE run_miss AS
SELECT dir, stock_empty_free_slot AS misses,
  switch_arrivals - (stock_empty_arrivals - stock_empty_free_slot) AS n,
  CASE WHEN connects_started > 0 THEN connecting_ns / connects_started END AS connect_mean_ns,
  CASE WHEN warm_mode = 'cold' THEN 0 ELSE warm_size END AS w,
  warm_ns / 1e9 AS warm_conn_s
FROM ok_runs WHERE policy LIKE 'remake-%';

-- P(Poisson(m) >= w), exact by the series; m = 0 gives 1 for w = 0, else 0.
CREATE OR REPLACE MACRO lr_poisson_tail(m, w) AS (
  CASE WHEN w <= 0 THEN 1.0 WHEN m <= 0 THEN 0.0
       ELSE greatest(0.0, 1 - coalesce(list_sum(list_transform(range(0, w), lambda k: exp(-m + k * ln(m) - lgamma(k + 1)))), 0)) END);

-- Isolation rows, per run and state kind (plan section 8 R1-R3, S1, S2). A hit counts as
-- found when the policy promises cleanliness for that pair of leases: every lease under
-- reset-always and remake-always, a change of tenant (or no previous tenant) otherwise.
-- Same-tenant hits are counted apart (reset-on-switch keeps state within a tenant by design).
-- `checked` counts rows where the column was checked (value >= 0).
CREATE OR REPLACE TABLE iso AS
WITH o AS (
  SELECT o.*, r.policy, (r.policy IN ('reset-always', 'remake-always') OR o.prev_tenant IS DISTINCT FROM o.tenant) AS promised
  FROM oracle o JOIN ok_runs r USING (dir)),
u AS (
  UNPIVOT (SELECT dir, lease, tenant, prev_tenant, promised,
                  -- one state column per line: the self-test's canary deletes one of these lines
                  prepared,
                  cursors,
                  listens,
                  advisory_locks,
                  settings_changed,
                  temp_relations,
                  role_changed,
                  canary_read,
                  probe_seed_inherited,
                  probe_custom_name
           FROM o)
  ON COLUMNS(* EXCLUDE (dir, lease, tenant, prev_tenant, promised)) INTO NAME kind VALUE v)
SELECT dir, kind,
  CASE WHEN kind = 'canary_read' THEN 'canary' WHEN kind LIKE 'probe_%' THEN 'residue' ELSE 'state' END AS class,
  count(*) FILTER (WHERE v >= 0) AS checked,
  count(*) FILTER (WHERE v > 0 AND promised) AS found,
  count(*) FILTER (WHERE v > 0 AND NOT promised) AS same_tenant
FROM u GROUP BY ALL
UNION ALL
-- S1 structural: a lease that follows another on the same connection without a completed
-- reset where the policy needs one; under remake policies any such sharing at all.
SELECT dir, 'structural_reset', 'structural',
  count(*) FILTER (WHERE prev_tenant >= 0 AND (policy LIKE 'remake-%' OR reset_between >= 0)),
  count(*) FILTER (WHERE prev_tenant >= 0 AND CASE
      WHEN policy = 'remake-always' THEN true
      WHEN policy = 'remake-on-switch' THEN prev_tenant <> tenant
      WHEN policy = 'reset-always' THEN reset_between = 0
      ELSE prev_tenant <> tenant AND reset_between = 0 END),
  count(*) FILTER (WHERE prev_tenant = tenant AND reset_between = 0 AND policy NOT IN ('reset-always', 'remake-always'))
FROM o GROUP BY dir
UNION ALL
-- R3 backend identity (remake policies only): one backend (pid, start) seen with two
-- tenants under remake-on-switch, or with two leases under remake-always.
SELECT dir, 'backend_identity', 'identity', count(*),
  count(*) FILTER (WHERE CASE WHEN policy = 'remake-always' THEN leases > 1 ELSE tenants > 1 END), 0
FROM (SELECT dir, policy, backend_pid, backend_start_ns, count(DISTINCT lease) AS leases, count(DISTINCT tenant) AS tenants
      FROM o WHERE backend_pid >= 0 AND policy LIKE 'remake-%' GROUP BY ALL)
GROUP BY dir
UNION ALL
-- The same from leases.csv, by pool connection id, where the run wrote it.
SELECT l.dir, 'connection_identity', 'identity', count(*),
  count(*) FILTER (WHERE CASE WHEN r.policy = 'remake-always' THEN l.leases > 1 ELSE l.tenants > 1 END), 0
FROM (SELECT dir, conn, count(DISTINCT lease) AS leases, count(DISTINCT tenant) AS tenants
      FROM leases WHERE conn >= 0 AND granted_ns >= 0 GROUP BY ALL) l
JOIN ok_runs r USING (dir) WHERE r.policy LIKE 'remake-%'
GROUP BY l.dir;
