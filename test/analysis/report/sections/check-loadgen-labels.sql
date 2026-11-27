-- requires: loadgen.completed
-- claim: load tool label contract: completed is exported for exactly the four outcomes ok, db-error, refused and lost, and every errors_by_class reason is a two-character SQLSTATE class.
-- applies-to: loadgen
-- applies-to-scenarios: load-*
-- A built-in check of scripts/report.sh. Returns the violating rows.
WITH want(outcome) AS (VALUES ('ok'), ('db-error'), ('refused'), ('lost')),
inst AS (SELECT DISTINCT run_id, instance FROM final WHERE component = 'loadgen')
SELECT i.run_id || ' ' || i.instance || ': completed has no row for outcome ' || w.outcome AS violation
FROM inst i CROSS JOIN want w
WHERE NOT EXISTS (SELECT 1 FROM final f WHERE f.run_id = i.run_id AND f.instance = i.instance
                  AND f.component = 'loadgen' AND f.metric = 'completed' AND f.outcome = w.outcome)
UNION ALL
SELECT DISTINCT run_id || ' ' || instance || ': completed has an unknown outcome ''' || outcome || ''''
FROM final WHERE component = 'loadgen' AND metric = 'completed' AND outcome NOT IN ('ok', 'db-error', 'refused', 'lost')
UNION ALL
SELECT DISTINCT run_id || ' ' || instance || ': errors_by_class reason ''' || reason || ''' is not a two-character SQLSTATE class'
FROM final WHERE component = 'loadgen' AND metric = 'errors_by_class' AND NOT regexp_full_match(reason, '[0-9A-Z]{2}')
ORDER BY 1;
