-- requires: admission.in_flight admission.tenant_in_flight admission.tenant_time_at_share admission.lease_starts engine.leases_started engine.clients_in_transaction workload.requests
-- claim: fair-held-transactions: in every flush the dispatcher's per-tenant in flight sums to its total, its lease starts equal the engine's, and the in-flight total is at least the clients the engine holds inside a transaction (a held transaction counts); at the end nothing is in flight, both tenants completed every request without error, and the tenant holding transactions spent time at its share.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH f AS (
    SELECT m.run_id, m.flush_seq,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'in_flight') AS in_flight,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'tenant_in_flight'), 0) AS per_tenant,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'lease_starts') AS starts,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_started') AS e_starts,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'clients_in_transaction') AS in_tx
    FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario = 'fair-held-transactions' GROUP BY m.run_id, m.flush_seq
)
SELECT run_id || ': flush ' || flush_seq || ': in flight ' || in_flight || ' (per tenant ' || per_tenant || '), in a transaction ' || in_tx || ', starts ' || starts || ' engine ' || e_starts
FROM f WHERE per_tenant <> in_flight OR starts <> e_starts OR in_flight < in_tx)
UNION ALL
(WITH e AS (
    SELECT r.run_id,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'admission' AND f.metric = 'in_flight'), -1) AS in_flight,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'admission' AND f.metric = 'tenant_time_at_share' AND f.tenant = 'tenant0'), 0) AS at_share,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'requests' AND f.outcome = 'ok'), 0) AS ok,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'requests' AND f.outcome <> 'ok'), 0) AS failed
    FROM runs r JOIN final f USING (run_id) WHERE r.scenario = 'fair-held-transactions' GROUP BY r.run_id
)
SELECT run_id || ': final in flight ' || in_flight || ', tenant0 time at share ' || at_share || ', requests ok ' || ok || ' failed ' || failed
FROM e WHERE in_flight <> 0 OR at_share < 1 OR ok < 1 OR failed <> 0)
UNION ALL
SELECT 'no run of fair-held-transactions' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'fair-held-transactions');
