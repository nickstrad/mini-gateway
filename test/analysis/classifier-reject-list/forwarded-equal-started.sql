-- requires: classifier.statements classifier.statements_by_class classifier.rejects wire.client_queries_seen fakepg.statements_started
-- claim: classifier-reject-list: the classifier classified every Query message the clients sent
-- (wire.client_queries_seen); rejects plus forwarded statements equal the statements classified;
-- the fake Postgres started exactly the forwarded statements (at least 1), so no refused one
-- reached it; and at least one statement was refused.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'classifier' AND metric = 'statements'), 0) AS classified,
           coalesce(sum(value) FILTER (WHERE component = 'classifier' AND metric = 'rejects'), 0) AS rejects,
           coalesce(sum(value) FILTER (WHERE component = 'classifier' AND metric = 'statements_by_class' AND outcome IN ('start-lease', 'continue-lease')), 0) AS forwarded,
           coalesce(sum(value) FILTER (WHERE component = 'wire' AND metric = 'client_queries_seen'), 0) AS sent,
           coalesce(sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'statements_started'), 0) AS started
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': classified ' || coalesce(p.classified, -1) || ', sent ' || coalesce(p.sent, -1) || ', rejects ' || coalesce(p.rejects, -1)
       || ', forwarded ' || coalesce(p.forwarded, -1) || ', fake statements ' || coalesce(p.started, -1)
FROM runs r LEFT JOIN per_run p USING (run_id)
WHERE r.scenario = 'classifier-reject-list'
  AND (p.run_id IS NULL OR p.classified <> p.sent OR p.rejects + p.forwarded <> p.classified
       OR p.started <> p.forwarded OR p.forwarded < 1 OR p.rejects < 1))
UNION ALL
SELECT 'no run of classifier-reject-list' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'classifier-reject-list');
