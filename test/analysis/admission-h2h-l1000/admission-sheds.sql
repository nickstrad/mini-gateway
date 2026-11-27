-- requires: admission.shed admission.rule engine.replies
-- claim: admission-h2h-l1000: sheds by reason at 10x: both rules shed by the rule, the plain FIFO arm never sheds by a rule (only by overflow), and every shed was answered with the 53M01 row (engine replies, reason shed, equal the admission sheds).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH mode AS (
    SELECT run_id, max(mode) FILTER (WHERE value = 1) AS rule FROM final WHERE component = 'admission' AND metric = 'rule' GROUP BY run_id
), per_run AS (
    SELECT run_id,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed' AND reason = 'rule'), 0) AS shed_rule,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed' AND reason = 'overflow'), 0) AS shed_overflow,
           coalesce(sum(value) FILTER (WHERE component = 'admission' AND metric = 'shed'), 0) AS shed,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'replies' AND reason = 'shed'), 0) AS replies
    FROM final GROUP BY run_id
)
SELECT r.run_id || ': rule ' || coalesce(m.rule, '?') || ', shed by rule ' || coalesce(p.shed_rule, -1) || ', by overflow ' || coalesce(p.shed_overflow, -1)
       || ', all ' || coalesce(p.shed, -1) || ', 53M01 replies ' || coalesce(p.replies, -1)
FROM runs r LEFT JOIN per_run p USING (run_id) LEFT JOIN mode m USING (run_id)
WHERE r.scenario = 'admission-h2h-l1000'
  AND (p.run_id IS NULL OR p.shed < 1 OR p.replies <> p.shed
       OR (m.rule IN ('timeout', 'control-law') AND p.shed_rule < 1)
       OR (m.rule = 'fifo' AND p.shed_rule <> 0)))
UNION ALL
SELECT 'no run of admission-h2h-l1000' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'admission-h2h-l1000');
