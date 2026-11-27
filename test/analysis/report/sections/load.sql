-- Read by scripts/report.sh once, after test/analysis/views.sql and preamble.sql over every
-- complete run: copies the rows into tables, so each check reads them without parsing the
-- CSV files again, and points the views of views.sql at the tables. final, final_hist and
-- final_sc are defined on the views by name, so they follow. The report then narrows the
-- views to one scenario's runs with select.sql before running that scenario's SQL files.
CREATE TABLE all_metrics AS SELECT * FROM metrics;
CREATE TABLE all_hist AS SELECT * FROM hist;
CREATE TABLE all_runs AS SELECT * FROM runs;
CREATE TABLE all_run_dirs AS SELECT * FROM run_dirs;
CREATE OR REPLACE VIEW metrics AS SELECT * FROM all_metrics;
CREATE OR REPLACE VIEW hist AS SELECT * FROM all_hist;
CREATE OR REPLACE VIEW runs AS SELECT * FROM all_runs;
CREATE OR REPLACE VIEW run_dirs AS SELECT * FROM all_run_dirs;
