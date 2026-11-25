-- Views over flushed CSV runs, schema version 1 (PLAN.md sections 7.7 and 7.8).
-- Set before reading this file:
--   SET VARIABLE metrics_files    = ['<run>/metrics.csv', ...];
--   SET VARIABLE histograms_files = ['<run>/histograms.csv', ...];
--   SET VARIABLE run_files        = ['<run>/run.csv', ...];
-- scripts/csv-assert.sh sets them from the run directories it is given. Column types are
-- written out; DuckDB reads an empty text field as NULL, so label columns are turned back
-- into ''.

CREATE OR REPLACE VIEW metrics AS
SELECT schema_version, coalesce(run_id, '') AS run_id, flush_seq, t_ns, coalesce(instance, '') AS instance,
       coalesce(component, '') AS component, coalesce(metric, '') AS metric, coalesce(kind, '') AS kind,
       coalesce(tenant, '') AS tenant, coalesce(outcome, '') AS outcome, coalesce(reason, '') AS reason,
       coalesce(mode, '') AS mode, value
FROM read_csv(getvariable('metrics_files'), header = true, delim = ',', quote = '"', escape = '"',
  columns = {'schema_version': 'INTEGER', 'run_id': 'VARCHAR', 'flush_seq': 'BIGINT', 't_ns': 'BIGINT',
             'instance': 'VARCHAR', 'component': 'VARCHAR', 'metric': 'VARCHAR', 'kind': 'VARCHAR',
             'tenant': 'VARCHAR', 'outcome': 'VARCHAR', 'reason': 'VARCHAR', 'mode': 'VARCHAR', 'value': 'BIGINT'});

CREATE OR REPLACE VIEW hist AS
SELECT schema_version, coalesce(run_id, '') AS run_id, flush_seq, t_ns, coalesce(instance, '') AS instance,
       coalesce(component, '') AS component, coalesce(metric, '') AS metric,
       coalesce(tenant, '') AS tenant, coalesce(outcome, '') AS outcome, coalesce(reason, '') AS reason,
       coalesce(mode, '') AS mode, coalesce(unit, '') AS unit, bucket_lo, bucket_hi, count
FROM read_csv(getvariable('histograms_files'), header = true, delim = ',', quote = '"', escape = '"',
  columns = {'schema_version': 'INTEGER', 'run_id': 'VARCHAR', 'flush_seq': 'BIGINT', 't_ns': 'BIGINT',
             'instance': 'VARCHAR', 'component': 'VARCHAR', 'metric': 'VARCHAR',
             'tenant': 'VARCHAR', 'outcome': 'VARCHAR', 'reason': 'VARCHAR', 'mode': 'VARCHAR',
             'unit': 'VARCHAR', 'bucket_lo': 'BIGINT', 'bucket_hi': 'BIGINT', 'count': 'BIGINT'});

CREATE OR REPLACE VIEW runs AS
SELECT schema_version, coalesce(run_id, '') AS run_id, coalesce(scenario, '') AS scenario, coalesce(tier, '') AS tier,
       seed, coalesce(arm, '') AS arm, instances, coalesce(build, '') AS build,
       coalesce(config_fingerprint, '') AS config_fingerprint, flushes, duration_ns
FROM read_csv(getvariable('run_files'), header = true, delim = ',', quote = '"', escape = '"',
  columns = {'schema_version': 'INTEGER', 'run_id': 'VARCHAR', 'scenario': 'VARCHAR', 'tier': 'VARCHAR',
             'seed': 'BIGINT', 'arm': 'VARCHAR', 'instances': 'BIGINT', 'build': 'VARCHAR',
             'config_fingerprint': 'VARCHAR', 'flushes': 'BIGINT', 'duration_ns': 'BIGINT'});

-- The last flush of each run.
CREATE OR REPLACE VIEW final AS
SELECT m.* FROM metrics m
JOIN (SELECT run_id, max(flush_seq) AS last_seq FROM metrics GROUP BY run_id) l
  ON m.run_id = l.run_id AND m.flush_seq = l.last_seq;

CREATE OR REPLACE VIEW final_hist AS
SELECT h.* FROM hist h
JOIN (SELECT run_id, max(flush_seq) AS last_seq FROM metrics GROUP BY run_id) l
  ON h.run_id = l.run_id AND h.flush_seq = l.last_seq;
