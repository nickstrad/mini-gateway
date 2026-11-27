-- Report views, read by scripts/report.sh after test/analysis/views.sql over the same runs.
-- run_dirs: one row per run.csv with the file it came from, so the report can pair a run
-- directory with the scenario its run.csv names. final_sc: the final flush with the scenario.
CREATE OR REPLACE VIEW run_dirs AS
SELECT coalesce(run_id, '') AS run_id, coalesce(scenario, '') AS scenario, filename
FROM read_csv(getvariable('run_files'), header = true, delim = ',', quote = '"', escape = '"', filename = true,
  columns = {'schema_version': 'INTEGER', 'run_id': 'VARCHAR', 'scenario': 'VARCHAR', 'tier': 'VARCHAR',
             'seed': 'BIGINT', 'arm': 'VARCHAR', 'instances': 'BIGINT', 'build': 'VARCHAR',
             'config_fingerprint': 'VARCHAR', 'flushes': 'BIGINT', 'duration_ns': 'BIGINT'});

CREATE OR REPLACE VIEW final_sc AS
SELECT r.scenario, f.* FROM final f JOIN runs r USING (run_id);
