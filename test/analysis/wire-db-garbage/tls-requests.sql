-- Chunk 4 wire scenario wire-db-garbage: TLS requests by kind and answer equal the requests sent and the answers given
-- Schema: PLAN.md section 7.7, version 1. Run by scripts/csv-assert.sh in the run directory.
-- Returns the rows that violate the claim; zero rows is a pass.
SELECT 'schema version ' || schema_version AS violation
FROM read_csv('metrics.csv') WHERE schema_version <> 1
UNION ALL
(WITH final AS (
    SELECT * FROM read_csv('metrics.csv')
    WHERE flush_seq = (SELECT max(flush_seq) FROM read_csv('metrics.csv'))
),
proxy AS (
    SELECT outcome, sum(value) AS value FROM final
    WHERE component = 'wire' AND metric = 'client_prestartup_requests' AND mode = 'tls' GROUP BY outcome
),
sent AS (
    SELECT sum(value) AS value FROM final WHERE component = 'workload' AND metric = 'startup_tls_requests' AND coalesce(tenant, '') = ''
),
answered AS (
    SELECT outcome, sum(value) AS value FROM final
    WHERE component = 'workload' AND metric = 'startup_tls_answered' AND coalesce(tenant, '') = '' GROUP BY outcome
),
db AS (
    SELECT outcome, sum(value) AS value FROM final WHERE component = 'wire' AND metric = 'database_tls_answers' GROUP BY outcome
),
fake AS (
    SELECT outcome, sum(value) AS value FROM final WHERE component = 'fakepg' AND metric = 'tls_answered' GROUP BY outcome
)
SELECT 'TLS requests answered by the proxy ' || coalesce((SELECT sum(value) FROM proxy WHERE outcome IN ('S', 'N')), 0)
       || ' != sent by the workload ' || coalesce((SELECT value FROM sent), 0) AS violation
WHERE coalesce((SELECT sum(value) FROM proxy WHERE outcome IN ('S', 'N')), 0) <> coalesce((SELECT value FROM sent), 0)
UNION ALL
SELECT 'answer ' || coalesce(p.outcome, a.outcome) || ': proxy ' || coalesce(p.value, 0) || ', workload ' || coalesce(a.value, 0)
FROM (SELECT * FROM proxy WHERE outcome IN ('S', 'N')) p FULL OUTER JOIN answered a ON p.outcome = a.outcome
WHERE coalesce(p.value, 0) <> coalesce(a.value, 0)
UNION ALL
SELECT 'database answer ' || coalesce(d.outcome, f.outcome) || ': proxy ' || coalesce(d.value, 0) || ', fakepg ' || coalesce(f.value, 0)
FROM (SELECT * FROM db WHERE outcome IN ('S', 'N')) d FULL OUTER JOIN fake f ON d.outcome = f.outcome
WHERE coalesce(d.value, 0) <> coalesce(f.value, 0));
