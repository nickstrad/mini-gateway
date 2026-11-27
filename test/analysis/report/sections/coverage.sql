-- "Coverage by scenario, statement class and outcome", from the final flush of every run.
-- One row per (table, scenario, label): table, scenario, order, label, total over the runs,
-- runs where the value is not 0, runs of the scenario. The labelled tables omit labels that
-- are 0 in every run of a scenario; engine-requests, loadgen-totals and loadgen-completed
-- always show every label for a scenario whose runs export the component.
WITH sc AS (SELECT scenario, count(*) AS n FROM runs GROUP BY scenario),
eng_runs AS (SELECT DISTINCT scenario, run_id FROM final_sc WHERE component = 'engine'),
eng_labels(ord, label) AS (VALUES (1, 'received'), (2, 'accepted'), (3, 'completed'), (4, 'failed'), (5, 'shed'), (6, 'rejected')),
eng_vals AS (
  SELECT run_id, substr(metric, length('requests_') + 1) AS label, sum(value) AS v
  FROM final WHERE component = 'engine' AND metric LIKE 'requests\_%' ESCAPE '\' GROUP BY ALL
),
eng AS (
  SELECT 'engine-requests' AS tbl, e.scenario, l.ord, l.label, e.run_id, coalesce(v.v, 0) AS v
  FROM eng_runs e CROSS JOIN eng_labels l LEFT JOIN eng_vals v ON v.run_id = e.run_id AND v.label = l.label
),
lab AS (
  SELECT 'engine-replies' AS tbl, scenario, 0 AS ord, reason AS label, run_id, sum(value) AS v FROM final_sc
  WHERE component = 'engine' AND metric = 'replies' GROUP BY ALL
  UNION ALL
  SELECT 'classifier-class', scenario, 0, outcome, run_id, sum(value) FROM final_sc
  WHERE component = 'classifier' AND metric = 'statements_by_class' GROUP BY ALL
  UNION ALL
  SELECT 'classifier-rejects', scenario, 0, reason, run_id, sum(value) FROM final_sc
  WHERE component = 'classifier' AND metric = 'rejects' GROUP BY ALL
  UNION ALL
  SELECT 'workload-requests', scenario, 0, outcome, run_id, sum(value) FROM final_sc
  WHERE component = 'workload' AND metric = 'requests' GROUP BY ALL
  UNION ALL
  SELECT 'workload-errors', scenario, 0, reason, run_id, sum(value) FROM final_sc
  WHERE component = 'workload' AND metric = 'errors_by_code' GROUP BY ALL
  UNION ALL
  SELECT 'loadgen-completed', scenario,
         CASE outcome WHEN 'ok' THEN 1 WHEN 'db-error' THEN 2 WHEN 'refused' THEN 3 WHEN 'lost' THEN 4 ELSE 5 END,
         outcome, run_id, sum(value) FROM final_sc
  WHERE component = 'loadgen' AND metric = 'completed' GROUP BY ALL
  UNION ALL
  SELECT 'loadgen-errors', scenario, 0, reason, run_id, sum(value) FROM final_sc
  WHERE component = 'loadgen' AND metric = 'errors_by_class' GROUP BY ALL
),
lg_run AS (
  SELECT scenario, run_id,
         coalesce(sum(value) FILTER (WHERE metric = 'intended'), 0) AS intended,
         coalesce(sum(value) FILTER (WHERE metric = 'sent'), 0) AS sent,
         coalesce(sum(value) FILTER (WHERE metric = 'could_not_send'), 0) AS could_not_send,
         coalesce(sum(value) FILTER (WHERE metric = 'late'), 0) AS late,
         coalesce(sum(value) FILTER (WHERE metric = 'queued'), 0) AS queued,
         coalesce(sum(value) FILTER (WHERE metric = 'in_flight'), 0) AS in_flight
  FROM final_sc WHERE component = 'loadgen' GROUP BY scenario, run_id
),
lg AS (
  SELECT 'loadgen-totals' AS tbl, scenario, 1 AS ord, 'intended' AS label, run_id, intended AS v FROM lg_run
  UNION ALL SELECT 'loadgen-totals', scenario, 2, 'sent', run_id, sent FROM lg_run
  UNION ALL SELECT 'loadgen-totals', scenario, 3, 'could_not_send', run_id, could_not_send FROM lg_run
  UNION ALL SELECT 'loadgen-totals', scenario, 4, 'late', run_id, late FROM lg_run
  UNION ALL SELECT 'loadgen-totals', scenario, 5, 'queued (final)', run_id, queued FROM lg_run
  UNION ALL SELECT 'loadgen-totals', scenario, 6, 'in_flight (final)', run_id, in_flight FROM lg_run
  UNION ALL SELECT 'loadgen-totals', scenario, 7, 'achieved on time (sent - late)', run_id, sent - late FROM lg_run
  UNION ALL SELECT 'loadgen-totals', scenario, 8, 'shortfall runs (late + could_not_send > 0)', run_id, CASE WHEN late + could_not_send > 0 THEN 1 ELSE 0 END FROM lg_run
),
allrows AS (SELECT * FROM eng UNION ALL SELECT * FROM lab UNION ALL SELECT * FROM lg)
SELECT a.tbl, a.scenario, a.ord, a.label, sum(a.v) AS total, count(*) FILTER (WHERE a.v <> 0) AS nonzero, sc.n
FROM allrows a JOIN sc USING (scenario)
GROUP BY a.tbl, a.scenario, a.ord, a.label, sc.n
HAVING sum(a.v) <> 0 OR count(*) FILTER (WHERE a.v <> 0) > 0 OR a.tbl IN ('engine-requests', 'loadgen-totals', 'loadgen-completed')
ORDER BY a.tbl, a.scenario, a.ord, a.label;
