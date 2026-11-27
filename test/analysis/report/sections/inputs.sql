-- "Inputs": one row per scenario, aggregated over its runs so it reads the same at 3 seeds
-- and at 300: scenario, tiers, runs, lowest seed, highest seed, arms, flushes, instances per run.
SELECT scenario,
       string_agg(DISTINCT tier, ',' ORDER BY tier) AS tiers,
       count(*) AS runs,
       min(seed) AS seed_lo,
       max(seed) AS seed_hi,
       string_agg(DISTINCT arm, ',' ORDER BY arm) AS arms,
       sum(flushes) AS flushes,
       string_agg(DISTINCT instances::VARCHAR, ',' ORDER BY instances::VARCHAR) AS instances
FROM runs
GROUP BY scenario
ORDER BY scenario;
