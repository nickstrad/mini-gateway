-- requires: pool.open_plus_connecting pool.open_plus_connecting_peak pool.capacity pool.dials pool.dials_opened pool.dial_failures pool.connecting pool.lent pool.checking pool.cleaning pool.checkout_attempts pool.checkouts
-- claim: pool-session-always: in every flush open + connecting <= the cap (also as a high-water mark), every dial ends opened, failed or still connecting, every checkout ends lent or refused, and the final flush shows nothing lent, checking or in clean-up; the pool dialled.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH f AS (
    SELECT m.run_id, m.flush_seq,
           sum(value) FILTER (WHERE metric = 'open_plus_connecting') AS opc,
           sum(value) FILTER (WHERE metric = 'open_plus_connecting_peak') AS peak,
           sum(value) FILTER (WHERE metric = 'capacity') AS cap,
           sum(value) FILTER (WHERE metric = 'dials') AS dials,
           sum(value) FILTER (WHERE metric = 'dials_opened') AS opened,
           sum(value) FILTER (WHERE metric = 'dial_failures') AS failed,
           sum(value) FILTER (WHERE metric = 'connecting') AS connecting,
           sum(value) FILTER (WHERE metric = 'checkout_attempts') AS attempts,
           sum(value) FILTER (WHERE metric = 'checkouts') AS checkouts
    FROM metrics m JOIN runs r USING (run_id)
    WHERE m.component = 'pool' AND r.scenario = 'pool-session-always' AND r.arm = 'base'
    GROUP BY m.run_id, m.flush_seq
)
SELECT run_id || ': flush ' || flush_seq || ': open+connecting ' || opc || ' (peak ' || peak || ') cap ' || cap || ', dials ' || dials || ' opened ' || opened || ' failed ' || failed || ' connecting ' || connecting || ', checkouts ' || attempts || ' outcomes ' || checkouts
FROM f WHERE opc > cap OR peak > cap OR dials <> opened + failed + connecting OR attempts <> checkouts)
UNION ALL
(WITH last AS (
    SELECT r.run_id,
           coalesce(sum(value) FILTER (WHERE component = 'pool' AND metric IN ('lent', 'checking', 'cleaning')), -1) AS busy,
           coalesce(sum(value) FILTER (WHERE component = 'pool' AND metric = 'dials'), 0) AS dials
    FROM runs r LEFT JOIN final f USING (run_id) WHERE r.scenario = 'pool-session-always' AND r.arm = 'base' GROUP BY r.run_id
)
SELECT run_id || ': final flush: lent+checking+cleaning ' || busy || ', dials ' || dials FROM last WHERE busy <> 0 OR dials < 1)
UNION ALL
SELECT 'no run of pool-session-always' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'pool-session-always');
