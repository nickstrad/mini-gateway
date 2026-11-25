-- Chunk 4 wire scenario wire-startup-plain: startup failures by reason equal the startup faults injected by kind
-- Schema: PLAN.md section 7.7, version 1. Run by scripts/csv-assert.sh in the run directory.
-- Returns the rows that violate the claim; zero rows is a pass.
SELECT 'schema version ' || schema_version AS violation
FROM read_csv('metrics.csv') WHERE schema_version <> 1
UNION ALL
SELECT 'no wire rows: the counters did not move' AS violation
WHERE NOT EXISTS (SELECT 1 FROM read_csv('metrics.csv') WHERE component = 'wire' AND metric = 'client_startups_begun' AND value > 0)
UNION ALL
(WITH final AS (
    SELECT * FROM read_csv('metrics.csv')
    WHERE flush_seq = (SELECT max(flush_seq) FROM read_csv('metrics.csv'))
),
expected(component, metric, reason, value) AS (
    SELECT * FROM (VALUES ('wire', 'client_startups_failed', 'none', 0)) WHERE false
),
actual AS (
    SELECT component, metric, reason, sum(value) AS value FROM final
    WHERE component = 'wire' AND metric IN ('client_startups_failed', 'database_startups_failed') AND value <> 0
    GROUP BY component, metric, reason
)
SELECT 'metric ' || coalesce(e.metric, a.metric) || ' reason ' || coalesce(e.reason, a.reason)
       || ': expected ' || coalesce(e.value, 0) || ', flushed ' || coalesce(a.value, 0) AS violation
FROM expected e FULL OUTER JOIN actual a ON e.metric = a.metric AND e.reason = a.reason
WHERE coalesce(e.value, 0) <> coalesce(a.value, 0));
