-- requires: loadgen.sent loadgen.completed loadgen.in_flight
-- claim: load tool conservation, per instance in the final flush: every sent request has a final outcome or is still in flight (sent = completed over ok, db-error, refused and lost + in_flight).
-- applies-to: loadgen
-- applies-to-scenarios: load-*
-- A built-in check of scripts/report.sh. Returns the violating rows.
WITH p AS (
  SELECT run_id, instance,
         coalesce(sum(value) FILTER (WHERE metric = 'sent'), 0) AS sent,
         coalesce(sum(value) FILTER (WHERE metric = 'completed'), 0) AS completed,
         coalesce(sum(value) FILTER (WHERE metric = 'in_flight'), 0) AS in_flight
  FROM final WHERE component = 'loadgen' GROUP BY run_id, instance
)
SELECT run_id || ' ' || instance || ': sent ' || sent || ', completed ' || completed || ', in_flight ' || in_flight AS violation
FROM p WHERE sent <> completed + in_flight
ORDER BY 1;
