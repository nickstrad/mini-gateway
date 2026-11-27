-- requires: engine.replies
-- claim: tierr-db-killed: tier R: one backend killed at each point gives exactly the predicted error-contract row once: lost-after-write, lost-after-read, partial-reply, lost-in-transaction.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'lost-after-write'), 0) AS w,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'lost-after-read'), 0) AS r,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'partial-reply'), 0) AS p,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'lost-in-transaction'), 0) AS tx
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': ' || 'w ' || coalesce(p.w, -1) || ', ' || 'r ' || coalesce(p.r, -1) || ', ' || 'p ' || coalesce(p.p, -1) || ', ' || 'tx ' || coalesce(p.tx, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'tierr-db-killed'
  AND (p.run_id IS NULL OR p.w <> 1 OR p.r <> 1 OR p.p <> 1 OR p.tx <> 1))
UNION ALL
SELECT 'no run of tierr-db-killed' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'tierr-db-killed');
