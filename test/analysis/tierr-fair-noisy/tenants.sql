-- requires: admission.tenant_pushes admission.tenant_served admission.lease_starts engine.leases_started admission.in_flight
-- claim: tierr-fair-noisy: against real Postgres, from the gateway's own files: the quiet tenant (tenant1) had more than 99% of its requests served, the flooder (tenant0) took more than 95% of the admission sheds, the dispatcher saw every lease the engine started, and nothing was in flight at the end.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH s AS (
    SELECT r.run_id,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'tenant_pushes' AND f.tenant = 'tenant1'), 0) AS q_pushes,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'tenant_served' AND f.tenant = 'tenant1'), 0) AS q_served,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'tenant_shed' AND f.tenant = 'tenant0' AND f.reason <> 'breaker'), 0) AS a_shed,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'tenant_shed' AND f.reason <> 'breaker'), 0) AS shed,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'lease_starts'), -1) AS starts,
           coalesce(sum(f.value) FILTER (WHERE f.metric = 'in_flight'), -1) AS in_flight
    FROM runs r JOIN final f USING (run_id)
    WHERE r.scenario = 'tierr-fair-noisy' AND f.component = 'admission' GROUP BY r.run_id
), e AS (
    SELECT r.run_id, coalesce(sum(f.value), -1) AS e_starts FROM runs r JOIN final f USING (run_id)
    WHERE r.scenario = 'tierr-fair-noisy' AND f.component = 'engine' AND f.metric = 'leases_started' GROUP BY r.run_id
)
SELECT s.run_id || ': quiet served ' || q_served || ' of ' || q_pushes || ', flooder shed ' || a_shed || ' of ' || shed || ', lease starts ' || starts
       || ' engine ' || e.e_starts || ', in flight ' || in_flight
FROM s JOIN e USING (run_id)
WHERE q_pushes < 1 OR 100 * q_served <= 99 * q_pushes OR shed < 1 OR 100 * a_shed <= 95 * shed OR starts <> e.e_starts OR in_flight <> 0)
UNION ALL
SELECT 'no run of tierr-fair-noisy' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'tierr-fair-noisy');
