-- requires: engine.stage_passed engine.retry_ends retry.attempts retry.retries fakepg.writes_applied workload.intended workload.requests
-- claim: retry-storm and retry-no-double-apply: the statements the proxy forwarded are exactly its first attempts plus its retries, less the retries refused at the forward because the breaker opened after the grant (with the policy on the retries are bounded by the budget, see budget.sql; with it off there are none), and the database applied no more writes than distinct writes were sent (each operation has its own statement text) and at least as many as were acknowledged; the retry-everything arm forwards more statements than first attempts (amplification) and the database applied more writes than distinct writes were sent (duplicate writes).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH f AS (
    SELECT run_id,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'stage_passed' AND mode = 'forward') AS forwards,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'attempts') AS attempts,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'retries') AS retries,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'retry_ends' AND outcome = 'breaker'), 0) AS refused_at_forward,
           sum(value) FILTER (WHERE component = 'fakepg' AND metric = 'writes_applied') AS applied,
           sum(value) FILTER (WHERE component = 'workload' AND metric = 'intended') AS distinct_sent,
           sum(value) FILTER (WHERE component = 'workload' AND metric = 'requests' AND outcome = 'ok') AS acked
    FROM final GROUP BY run_id
), j AS (SELECT f.*, r.arm, r.scenario, r.seed FROM f JOIN runs r USING (run_id))
SELECT run_id || ': forwards ' || forwards || ' attempts ' || attempts || ' retries ' || retries || ' applied ' || applied
       || ' distinct sent ' || distinct_sent || ' acknowledged ' || acked || ' (arm ' || arm || ')'
FROM j
WHERE scenario IN ('retry-storm', 'retry-no-double-apply')
  AND (forwards IS NULL OR (arm <> 'everything' AND (forwards < attempts OR forwards > attempts + retries - refused_at_forward))
       OR (arm <> 'everything' AND (applied > distinct_sent OR applied < acked)) OR acked < 1)
UNION ALL
SELECT run_id || ': retry-everything forwarded ' || forwards || ' statements for ' || attempts || ' first attempts: no amplification'
FROM j WHERE scenario IN ('retry-storm', 'retry-no-double-apply') AND arm = 'everything' AND forwards <= attempts
UNION ALL
SELECT run_id || ': retry-everything applied ' || applied || ' writes for ' || distinct_sent || ' distinct writes sent: no duplicate write'
FROM j WHERE scenario IN ('retry-storm', 'retry-no-double-apply') AND arm = 'everything' AND applied <= distinct_sent)
UNION ALL
SELECT 'no run of retry-storm' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'retry-storm');
