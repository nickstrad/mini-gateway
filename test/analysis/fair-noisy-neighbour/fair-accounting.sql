-- requires: admission.in_flight admission.lease_starts admission.lease_ends admission.limit admission.rounds engine.leases_started engine.leases_ended
-- claim: fair-noisy-neighbour: in every flush of the fair arm the dispatcher's lease starts and ends equal the engine's, in flight = starts - ends <= the limit, and the per-tenant in flight sums to the total; the final flush has nothing in flight and the dispatcher gave turns.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH f AS (
    SELECT m.run_id, m.flush_seq,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'in_flight') AS in_flight,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'limit') AS lim,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'lease_starts') AS starts,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'lease_ends') AS ends,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_started') AS e_starts,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'leases_ended') AS e_ends,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'tenant_in_flight'), 0) AS per_tenant,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'rounds') AS rounds
    FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario = 'fair-noisy-neighbour' AND r.arm = 'fair' GROUP BY m.run_id, m.flush_seq
)
SELECT run_id || ': flush ' || flush_seq || ': in flight ' || in_flight || ' (per tenant ' || per_tenant || ', limit ' || lim || '), starts ' || starts
       || ' ends ' || ends || ', engine ' || e_starts || ' / ' || e_ends
FROM f WHERE starts <> e_starts OR ends <> e_ends OR in_flight <> starts - ends OR in_flight > lim OR per_tenant <> in_flight)
UNION ALL
(SELECT r.run_id || ': final flush in flight ' || coalesce(sum(f.value) FILTER (WHERE f.metric = 'in_flight'), -1) || ', rounds ' || coalesce(sum(f.value) FILTER (WHERE f.metric = 'rounds'), -1)
FROM runs r JOIN final f USING (run_id)
WHERE r.scenario = 'fair-noisy-neighbour' AND r.arm = 'fair' AND f.component = 'admission'
GROUP BY r.run_id
HAVING coalesce(sum(f.value) FILTER (WHERE f.metric = 'in_flight'), -1) <> 0 OR coalesce(sum(f.value) FILTER (WHERE f.metric = 'rounds'), 0) < 1)
UNION ALL
SELECT 'no fair run of fair-noisy-neighbour' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'fair-noisy-neighbour' AND arm = 'fair');
