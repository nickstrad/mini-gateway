-- Read by scripts/report.sh after test/analysis/guard.sql, in the same session. Each row is a
-- reason to fail: a run whose metrics.csv does not hold every flush its run.csv declares (a
-- whole flush missing, the final one included), as csvflush.Reader checks it.
SELECT 'run ' || r.run_id || ': run.csv says ' || r.flushes || ' flushes, metrics.csv has ' || coalesce(m.n, 0)
FROM runs r LEFT JOIN (SELECT run_id, count(DISTINCT flush_seq) AS n FROM metrics GROUP BY run_id) m ON m.run_id = r.run_id
WHERE coalesce(m.n, 0) <> r.flushes
ORDER BY 1;
