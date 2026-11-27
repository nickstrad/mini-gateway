-- requires: loadgen.intended
-- claim: load tool: counters must move; every run intended at least one request (intended > 0).
-- applies-to: loadgen
-- applies-to-scenarios: load-*
-- A built-in check of scripts/report.sh. Returns the violating rows.
SELECT r.run_id || ': intended ' || coalesce(sum(f.value), 0) AS violation
FROM runs r LEFT JOIN final f ON f.run_id = r.run_id AND f.component = 'loadgen' AND f.metric = 'intended'
GROUP BY r.run_id HAVING coalesce(sum(f.value), 0) <= 0
ORDER BY 1;
