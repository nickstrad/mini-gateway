-- requires: loadgen.queued
-- claim: load tool: nothing is left queued in the final flush (queued = 0).
-- applies-to: loadgen
-- applies-to-scenarios: load-*
-- A built-in check of scripts/report.sh. Returns the violating rows.
SELECT run_id || ' ' || instance || ': queued ' || sum(value) AS violation
FROM final WHERE component = 'loadgen' AND metric = 'queued'
GROUP BY run_id, instance HAVING sum(value) <> 0
ORDER BY 1;
