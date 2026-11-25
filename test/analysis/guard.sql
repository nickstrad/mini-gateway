-- Schema version 1 guard, run by scripts/csv-assert.sh once over all the run directories it
-- was given, after views.sql. Needs SET VARIABLE expected_runs = <number of run directories>.
-- Each row returned is a reason to fail: a schema_version other than 1, an empty field in a
-- column that may not be empty, a run directory without exactly one run.csv row or with a
-- run id another directory also has, a run without metric rows, rows of a run id no run.csv
-- names.
SELECT 'unknown schema version ' || coalesce(schema_version::VARCHAR, '(empty)') || ' in metrics.csv of run ' || run_id AS violation
FROM (SELECT DISTINCT schema_version, run_id FROM metrics WHERE schema_version IS DISTINCT FROM 1)
UNION ALL
SELECT 'unknown schema version ' || coalesce(schema_version::VARCHAR, '(empty)') || ' in histograms.csv of run ' || run_id
FROM (SELECT DISTINCT schema_version, run_id FROM hist WHERE schema_version IS DISTINCT FROM 1)
UNION ALL
SELECT 'unknown schema version ' || coalesce(schema_version::VARCHAR, '(empty)') || ' in run.csv of run ' || run_id
FROM runs WHERE schema_version IS DISTINCT FROM 1
UNION ALL
SELECT 'empty field in a required column of metrics.csv: ' || count(*) || ' rows, first in run ' || min(run_id)
FROM metrics
WHERE run_id = '' OR flush_seq IS NULL OR t_ns IS NULL OR component = '' OR metric = ''
   OR kind NOT IN ('counter', 'gauge') OR value IS NULL
HAVING count(*) > 0
UNION ALL
SELECT 'empty field in a required column of histograms.csv: ' || count(*) || ' rows, first in run ' || min(run_id)
FROM hist
WHERE run_id = '' OR flush_seq IS NULL OR t_ns IS NULL OR component = '' OR metric = ''
   OR unit NOT IN ('ns', 'bytes', 'count') OR bucket_lo IS NULL OR bucket_hi IS NULL OR count IS NULL
HAVING count(*) > 0
UNION ALL
SELECT 'empty field in a required column of run.csv of run ' || run_id
FROM runs
WHERE run_id = '' OR seed IS NULL OR instances IS NULL OR flushes IS NULL OR duration_ns IS NULL
UNION ALL
SELECT 'run.csv files hold ' || count(*) || ' rows with ' || count(DISTINCT run_id) || ' distinct run ids for '
       || getvariable('expected_runs') || ' run directories'
FROM runs
HAVING count(*) <> getvariable('expected_runs') OR count(DISTINCT run_id) <> getvariable('expected_runs')
UNION ALL
SELECT 'run ' || r.run_id || ': no rows in metrics.csv'
FROM runs r WHERE NOT EXISTS (SELECT 1 FROM metrics m WHERE m.run_id = r.run_id)
UNION ALL
SELECT 'metrics.csv rows of run ' || m.run_id || ' have no run.csv row'
FROM (SELECT DISTINCT run_id FROM metrics) m WHERE NOT EXISTS (SELECT 1 FROM runs r WHERE r.run_id = m.run_id)
UNION ALL
SELECT 'histograms.csv rows of run ' || h.run_id || ' have no run.csv row'
FROM (SELECT DISTINCT run_id FROM hist) h WHERE NOT EXISTS (SELECT 1 FROM runs r WHERE r.run_id = h.run_id);
