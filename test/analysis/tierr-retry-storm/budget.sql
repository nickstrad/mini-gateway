-- requires: retry.policy retry.attempts retry.retries retry.budget_level retry.budget_floor retry.budget_cap
-- claim: tierr-retry-storm (real Postgres, backends terminated at random), both policies (told apart by retry.policy, since the binary's runs carry no arm): in every flush window, retries times ten are at most the first attempts of the window plus the budget (in tenths) carried into it, which is at most the cap; from the start of the run, at most a tenth of the first attempts plus the floor; the level never passes the cap. With the policy off there is no retry at all.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH w AS (
    SELECT m.run_id, r.arm, m.flush_seq,
           max(value) FILTER (WHERE metric = 'retries') AS retries,
           max(value) FILTER (WHERE metric = 'attempts') AS attempts,
           max(value) FILTER (WHERE metric = 'budget_level') AS level,
           max(value) FILTER (WHERE metric = 'budget_floor') AS floor,
           max(value) FILTER (WHERE metric = 'budget_cap') AS cap,
           max(value) FILTER (WHERE metric = 'policy') AS policy
    FROM metrics m JOIN runs r USING (run_id)
    WHERE r.scenario IN ('tierr-retry-storm') AND m.component = 'retry'
    GROUP BY m.run_id, r.arm, m.flush_seq
), d AS (
    SELECT *, retries - lag(retries) OVER (PARTITION BY run_id ORDER BY flush_seq) AS d_retries,
              attempts - lag(attempts) OVER (PARTITION BY run_id ORDER BY flush_seq) AS d_attempts,
              lag(level) OVER (PARTITION BY run_id ORDER BY flush_seq) AS carried
    FROM w
)
SELECT run_id || ': flush ' || flush_seq || ' window: ' || d_retries || ' retries for ' || d_attempts || ' first attempts with ' || carried || ' tenths carried in'
FROM d WHERE policy = 1 AND d_retries IS NOT NULL AND (10 * d_retries > d_attempts + carried OR carried > cap)
UNION ALL
SELECT run_id || ': flush ' || flush_seq || ': ' || retries || ' retries for ' || attempts || ' first attempts and the floor ' || floor
FROM d WHERE policy = 1 AND (10 * retries > attempts + floor OR level > cap)
UNION ALL
SELECT run_id || ': ' || retries || ' retries with the policy off' FROM d WHERE policy = 0 AND retries <> 0)
UNION ALL
SELECT 'no run of tierr-retry-storm' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'tierr-retry-storm');
