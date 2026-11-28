-- Table 1: calibration — what a reset, a connect, a close and a first statement cost here.
-- Reads: `calibration` (calibration.json, schema mgcalib-calibration/1), bridge path only.
-- Keys: quantity, condition. No run directory is read.
-- Formulas: the file's own nearest-rank statistics over all included samples (ns), copied;
--   the connect ceiling is the M7S sustained rate's p50 over uncontended cells (connects/s);
--   CPU per connect is the median over uncontended, non-excluded M7S cells.
WITH c AS (SELECT * FROM cal_costs WHERE path = 'bridge' AND field = 'ns')
SELECT 'reset, clean session' AS quantity, cond AS condition, n, p50 AS p50_ns, p90 AS p90_ns, p99 AS p99_ns, 'M1' AS source
FROM c WHERE measurement = 'M1' AND cond = 'discard'
UNION ALL SELECT 'reset after one statement of kind', cond, n, p50, p90, p99, 'M2I discard' FROM c WHERE measurement = 'M2I' AND part = 'discard'
UNION ALL SELECT 'reset after a lease ending in a transaction', cond, n, p50, p90, p99, 'M2I total' FROM c WHERE measurement = 'M2I' AND part = 'total'
UNION ALL SELECT 'connect', cond, n, p50, p90, p99, 'M3' FROM c WHERE measurement = 'M3' AND part = 'connect'
UNION ALL SELECT 'close until the backend is gone (upper bound)', cond, n, p50, p90, p99, 'M4' FROM c WHERE measurement = 'M4' AND cond = 'poll' AND part = 'close_to_gone'
UNION ALL SELECT 'first statement ' || cond, part, n, p50, p90, p99, measurement FROM c WHERE measurement IN ('M5', 'M6')
UNION ALL SELECT 'sustained connect rate (per s)', cond, n_uncontended, p50_uncontended_milli // 1000, NULL, NULL, 'M7S uncontended' FROM cal_rates WHERE measurement = 'M7S' AND path = 'bridge'
UNION ALL SELECT 'burst connect rate (per s)', cond, n_uncontended, p50_uncontended_milli // 1000, NULL, NULL, 'M7B uncontended' FROM cal_rates WHERE measurement = 'M7B' AND path = 'bridge'
UNION ALL SELECT 'connect ceiling used by the report (per s)', getvariable('ceiling_cond'), NULL, ceiling_milli // 1000, NULL, NULL, 'M7S' FROM cal
UNION ALL SELECT 'Postgres CPU per connect-and-close', 'M7S uncontended', NULL, pg_cpu_per_connect_ns::BIGINT, NULL, NULL, 'cells' FROM cal
UNION ALL SELECT 'client CPU per connect-and-close', 'M7S uncontended', NULL, client_cpu_per_connect_ns::BIGINT, NULL, NULL, 'cells' FROM cal
UNION ALL SELECT 'calibration id', calibration_id, NULL, NULL, NULL, NULL, 'calibration.json' FROM cal_id
ORDER BY source, quantity, condition;
