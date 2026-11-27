-- requires: admission.queued_peak admission.shed admission.rule workload.not_yet_sent
-- claim: admission-anti-bloat: depth is the same at T (2 s) and 4T (8 s) under both rules (the peak queue depth does not move after T) and both rules shed by the rule, while the FIFO control with the same seed grows without bound (the clients' unsent backlog at 4T is at least three times the one at T) and sheds nothing by a rule.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH mode AS (
    SELECT run_id, max(mode) FILTER (WHERE value = 1) AS rule FROM final WHERE component = 'admission' AND metric = 'rule' GROUP BY run_id
), pts AS (
    SELECT run_id,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'queued_peak' AND t_ns = 2000000000) AS peak_t,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'queued_peak' AND t_ns = 8000000000) AS peak_4t,
           sum(value) FILTER (WHERE component = 'workload' AND metric = 'not_yet_sent' AND t_ns = 2000000000) AS backlog_t,
           sum(value) FILTER (WHERE component = 'workload' AND metric = 'not_yet_sent' AND t_ns = 8000000000) AS backlog_4t,
           sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed' AND reason = 'rule' AND t_ns = 8000000000) AS shed_rule
    FROM metrics GROUP BY run_id
)
SELECT r.run_id || ': rule ' || coalesce(m.rule, '?') || ', depth peak ' || coalesce(a.peak_t, -1) || ' at T, ' || coalesce(a.peak_4t, -1)
       || ' at 4T; backlog ' || coalesce(a.backlog_t, -1) || ' at T, ' || coalesce(a.backlog_4t, -1) || ' at 4T; shed by the rule ' || coalesce(a.shed_rule, -1)
FROM runs r LEFT JOIN pts a USING (run_id) LEFT JOIN mode m USING (run_id)
WHERE r.scenario = 'admission-anti-bloat'
  AND (a.run_id IS NULL OR a.peak_t IS NULL OR a.peak_4t IS NULL
       OR (m.rule IN ('timeout', 'control-law') AND (a.peak_t <> a.peak_4t OR a.shed_rule < 1))
       OR (m.rule = 'fifo' AND (a.backlog_t < 1 OR a.backlog_4t < 3 * a.backlog_t OR a.shed_rule <> 0))))
UNION ALL
SELECT 'no run of admission-anti-bloat' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'admission-anti-bloat');
