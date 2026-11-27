-- One row per run directory: scenario (from run.csv), the run.csv file, and the components
-- of its final flush (comma-separated, sorted). scripts/report.sh pairs scenarios with their
-- SQL files from this.
SELECT coalesce(nullif(d.scenario, ''), '(no scenario)') AS scenario, d.filename,
       coalesce((SELECT string_agg(DISTINCT f.component, ',' ORDER BY f.component) FROM final f WHERE f.run_id = d.run_id), '') AS components
FROM run_dirs d
ORDER BY d.scenario, d.filename;
