-- requires: classifier.rejects classifier.statements wire.client_queries_seen
-- claim: classifier-reject-only: every Query message the simulated clients sent (counted by the
-- gateway's framer, wire.client_queries_seen, at least 1) was rejected by the classifier, and the
-- classifier classified exactly those messages.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'classifier' AND metric = 'rejects'), 0) AS rejects,
           coalesce(sum(value) FILTER (WHERE component = 'classifier' AND metric = 'statements'), 0) AS classified,
           coalesce(sum(value) FILTER (WHERE component = 'wire' AND metric = 'client_queries_seen'), 0) AS sent
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': rejects ' || coalesce(p.rejects, -1) || ', classified ' || coalesce(p.classified, -1) || ', statements sent ' || coalesce(p.sent, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'classifier-reject-only'
  AND (p.run_id IS NULL OR p.sent < 1 OR p.rejects <> p.sent OR p.classified <> p.sent))
UNION ALL
SELECT 'no run of classifier-reject-only' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'classifier-reject-only');
