-- requires: workload.requests admission.shed engine.replies
-- claim: fair-many-tenants: in the fair arm the flooder (tenant0) took more than 95% of all shedding: its 53M01 errors against every tenant's 53M01 errors, read from the clients' own counts; and the admission core's sheds equal the engine's 53M01 replies.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH s AS (
    SELECT r.run_id,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'errors_by_code' AND f.reason = '53M01' AND f.tenant LIKE 'tenant0%'), 0) AS flood,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'errors_by_code' AND f.reason = '53M01'), 0) AS shed,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'admission' AND f.metric = 'shed' AND f.reason <> 'breaker'), 0) AS core,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'engine' AND f.metric = 'replies' AND f.reason = 'shed'), 0) AS replies
    FROM runs r JOIN final f USING (run_id)
    WHERE r.scenario = 'fair-many-tenants' AND r.arm = 'fair' GROUP BY r.run_id
)
SELECT run_id || ': the flooder took ' || flood || ' of ' || shed || ' sheds; core sheds ' || core || ', 53M01 replies ' || replies
FROM s WHERE shed < 1 OR 100 * flood <= 95 * shed OR core <> replies)
UNION ALL
SELECT 'no fair run of fair-many-tenants' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'fair-many-tenants' AND arm = 'fair');
