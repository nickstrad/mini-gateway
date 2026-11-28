-- requires: admission.in_flight admission.lease_starts admission.lease_ends admission.rounds admission.tenant_served engine.leases_started engine.leases_ended engine.requests_completed
-- claim: fair-results: with the fair dispatcher on (three tenants, transactions held open) in every flush the dispatcher's lease starts and ends equal the engine's and in flight = starts - ends; at the end nothing is in flight, the dispatcher gave turns, all three tenants were served and requests completed.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH f AS (
    SELECT m.run_id, m.flush_seq,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'in_flight') AS in_flight,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'lease_starts') AS starts,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'lease_ends') AS ends,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_started') AS e_starts,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_ended') AS e_ends
    FROM metrics m JOIN runs r USING (run_id) WHERE r.scenario = 'fair-results' GROUP BY m.run_id, m.flush_seq
)
SELECT run_id || ': flush ' || flush_seq || ': in flight ' || in_flight || ', starts ' || starts || ' ends ' || ends || ', engine ' || e_starts || ' / ' || e_ends
FROM f WHERE starts <> e_starts OR ends <> e_ends OR in_flight <> starts - ends)
UNION ALL
(WITH e AS (
    SELECT r.run_id,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'admission' AND f.metric = 'in_flight'), -1) AS in_flight,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'admission' AND f.metric = 'rounds'), 0) AS rounds,
           count(DISTINCT f.tenant) FILTER (WHERE f.component = 'admission' AND f.metric = 'tenant_served' AND f.value > 0) AS served_tenants,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'engine' AND f.metric = 'requests_completed'), 0) AS completed
    FROM runs r JOIN final f USING (run_id) WHERE r.scenario = 'fair-results' GROUP BY r.run_id
)
SELECT run_id || ': final in flight ' || in_flight || ', rounds ' || rounds || ', tenants served ' || served_tenants || ', completed ' || completed
FROM e WHERE in_flight <> 0 OR rounds < 1 OR served_tenants <> 3 OR completed < 1)
UNION ALL
SELECT 'no run of fair-results' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'fair-results');
