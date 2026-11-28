-- requires: coalescer.offered coalescer.flights_started coalescer.hits coalescer.refused coalescer.passed coalescer.waiters_in coalescer.waiter_ends coalescer.waiting coalescer.flight_ends coalescer.flights engine.requests_accepted engine.requests_coalesced
-- claim: coalesce-shared-error-strict: every shareable request ends started, hit, refused or passed; every waiter ends answered, refused, left or gone; every flight ends; the final flush shows no flight, waiter, stored byte or listed key, and the engine's conservation balances with the waiters counted; the coalescer was offered work.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH c AS (
    SELECT r.run_id,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'offered') AS offered,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric IN ('flights_started', 'hits', 'refused', 'passed')) AS decided,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'waiters_in') AS waiters_in,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric IN ('waiter_ends', 'waiting')) AS waiters_out,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric = 'flights_started') AS started,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric IN ('flight_ends', 'flights')) AS flights_out,
           sum(value) FILTER (WHERE component = 'coalescer' AND metric IN ('waiting', 'flights', 'table_size', 'stored_bytes', 'no_share')) AS left_over,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'requests_accepted') AS accepted,
           sum(value) FILTER (WHERE component = 'engine' AND metric IN ('requests_completed', 'requests_failed', 'requests_shed',
                                                                        'requests_queued', 'requests_in_flight', 'requests_coalesced')) AS ended
    FROM runs r JOIN final f USING (run_id) WHERE r.scenario = 'coalesce-shared-error-strict' AND r.arm = 'base' GROUP BY r.run_id
)
SELECT run_id || ': offered ' || offered || ' decided ' || decided || ', waiters in ' || waiters_in || ' out ' || waiters_out
       || ', flights ' || started || ' ended or live ' || flights_out || ', left over ' || left_over || ', accepted ' || accepted || ' ended ' || ended
FROM c WHERE offered IS DISTINCT FROM decided OR waiters_in IS DISTINCT FROM waiters_out OR started IS DISTINCT FROM flights_out
   OR left_over <> 0 OR accepted IS DISTINCT FROM ended OR offered < 1)
UNION ALL
SELECT 'no base run of coalesce-shared-error-strict' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'coalesce-shared-error-strict' AND arm = 'base');
