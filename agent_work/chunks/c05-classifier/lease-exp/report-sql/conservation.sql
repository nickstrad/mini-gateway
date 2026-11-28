-- requires: pool.leases_requested pool.leases_granted pool.leases_released pool.connects pool.closes_done pool.conn_ns_total
-- claim: in every flush of every lease-lab run the pool's accounting is conserved:
--   requested = granted + waiting + pending + refused; granted = released + lent;
--   connects ok = closes done + open; the time-in-state integrals sum to the connection time.
-- Lives one level below test/analysis/lease/ so scripts/csv-assert.sh's root mode does not take
-- "lease" for a scenario directory.
WITH p AS (
  SELECT run_id, flush_seq,
         sum(value) FILTER (WHERE metric = 'leases_requested') AS requested,
         sum(value) FILTER (WHERE metric = 'leases_granted') AS granted,
         sum(value) FILTER (WHERE metric = 'leases_released') AS released,
         sum(value) FILTER (WHERE metric = 'leases_refused') AS refused,
         sum(value) FILTER (WHERE metric = 'waiters') AS waiters,
         sum(value) FILTER (WHERE metric = 'pending') AS pending,
         sum(value) FILTER (WHERE metric = 'lent') AS lent,
         sum(value) FILTER (WHERE metric = 'connects' AND outcome = 'ok') AS connects_ok,
         sum(value) FILTER (WHERE metric = 'closes_done') AS closes_done,
         sum(value) FILTER (WHERE metric = 'open') AS open,
         sum(value) FILTER (WHERE metric IN ('lent_ns_total', 'warm_ns_total', 'connecting_ns_total', 'priming_ns_total',
                                            'resetting_ns_total', 'closing_ns_total', 'idle_ns_total')) AS states_ns,
         sum(value) FILTER (WHERE metric = 'conn_ns_total') AS conn_ns
  FROM metrics WHERE component = 'pool' GROUP BY run_id, flush_seq)
SELECT run_id || ': flush ' || flush_seq || ': requested ' || requested || ' <> granted ' || granted || ' + waiting ' || waiters
       || ' + pending ' || pending || ' + refused ' || refused AS violation
FROM p WHERE requested IS DISTINCT FROM granted + waiters + pending + refused
UNION ALL
SELECT run_id || ': flush ' || flush_seq || ': granted ' || granted || ' <> released ' || released || ' + lent ' || lent
FROM p WHERE granted IS DISTINCT FROM released + lent
UNION ALL
SELECT run_id || ': flush ' || flush_seq || ': connects ok ' || connects_ok || ' <> closes done ' || closes_done || ' + open ' || open
FROM p WHERE connects_ok IS DISTINCT FROM closes_done + open
UNION ALL
SELECT run_id || ': flush ' || flush_seq || ': time in states ' || states_ns || ' <> connection time ' || conn_ns
FROM p WHERE states_ns IS DISTINCT FROM conn_ns
UNION ALL
SELECT run_id || ': no flush has a lease' FROM (SELECT run_id, max(requested) AS r FROM p GROUP BY run_id) WHERE r IS NULL OR r = 0;
