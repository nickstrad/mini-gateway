-- requires: loadgen.intended loadgen.sent loadgen.could_not_send loadgen.queued
-- claim: load tool conservation, per instance in the final flush: every intended request was sent, could not be sent, or is still queued (intended = sent + could_not_send + queued).
-- applies-to: loadgen
-- applies-to-scenarios: load-*
-- A built-in check of scripts/report.sh. Returns the violating rows.
WITH p AS (
  SELECT run_id, instance,
         coalesce(sum(value) FILTER (WHERE metric = 'intended'), 0) AS intended,
         coalesce(sum(value) FILTER (WHERE metric = 'sent'), 0) AS sent,
         coalesce(sum(value) FILTER (WHERE metric = 'could_not_send'), 0) AS could_not_send,
         coalesce(sum(value) FILTER (WHERE metric = 'queued'), 0) AS queued
  FROM final WHERE component = 'loadgen' GROUP BY run_id, instance
)
SELECT run_id || ' ' || instance || ': intended ' || intended || ', sent ' || sent || ', could_not_send ' || could_not_send || ', queued ' || queued AS violation
FROM p WHERE intended <> sent + could_not_send + queued
ORDER BY 1;
