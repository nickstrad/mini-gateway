-- Read by scripts/report.sh before a scenario's SQL files, after SET VARIABLE scenario:
-- narrows the views to the runs whose run.csv names that scenario, so a SQL file sees exactly
-- the runs csv-assert.sh would give it. With the variable set to NULL every run is selected.
CREATE OR REPLACE VIEW run_dirs AS SELECT * FROM all_run_dirs
  WHERE getvariable('scenario') IS NULL OR coalesce(nullif(scenario, ''), '(no scenario)') = getvariable('scenario');
CREATE OR REPLACE VIEW metrics AS SELECT * FROM all_metrics WHERE run_id IN (SELECT run_id FROM run_dirs);
CREATE OR REPLACE VIEW hist AS SELECT * FROM all_hist WHERE run_id IN (SELECT run_id FROM run_dirs);
CREATE OR REPLACE VIEW runs AS SELECT * FROM all_runs WHERE run_id IN (SELECT run_id FROM run_dirs);
