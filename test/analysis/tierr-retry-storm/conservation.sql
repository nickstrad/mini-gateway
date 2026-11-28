-- requires: retry.offered retry.retries retry.refused retry.exhaustions retry.deposits retry.withdrawals retry.expired retry.budget_level engine.retry_offered engine.retry_asked engine.retried_requests engine.retry_ends engine.retries_pending engine.retry_rows
-- claim: tierr-retry-storm (real Postgres), both policies, final flush: every failure offered to the policy was retried or refused for one reason; the engine's offers and grants equal the policy's; deposits minus withdrawals minus credit lost at the cap equal the level; every request run again ended in exactly one way and none is pending; exhaustions are the refusals by reason budget. Retries happen only on the error row that allows one (lost-after-read).
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH f AS (
    SELECT run_id,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'offered') AS offered,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'retries') AS retries,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'refused') AS refused,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'refused' AND reason = 'budget') AS refused_budget,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'exhaustions') AS exhaustions,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'deposits') AS deposits,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'withdrawals') AS withdrawals,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'expired') AS expired,
           sum(value) FILTER (WHERE component = 'retry' AND metric = 'budget_level') AS level,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'retry_offered') AS e_offered,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'retry_asked') AS e_asked,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'retried_requests') AS e_retried,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'retry_ends') AS e_ended,
           sum(value) FILTER (WHERE component = 'engine' AND metric = 'retries_pending') AS e_pending,
           coalesce(sum(value) FILTER (WHERE component = 'engine' AND metric = 'retry_rows' AND reason <> 'lost-after-read'), 0) AS forbidden
    FROM final GROUP BY run_id
)
SELECT f.run_id || ': offered ' || f.offered || ' retries ' || f.retries || ' refused ' || f.refused || ' (budget ' || f.refused_budget
       || ', exhaustions ' || f.exhaustions || '); engine offered ' || f.e_offered || ' asked ' || f.e_asked || ' retried requests '
       || f.e_retried || ' ended ' || f.e_ended || ' pending ' || f.e_pending || '; deposits ' || f.deposits || ' withdrawals '
       || f.withdrawals || ' expired ' || f.expired || ' level ' || f.level || '; forbidden-row retries ' || f.forbidden || ' (arm ' || r.arm || ')'
FROM f JOIN runs r USING (run_id)
WHERE r.scenario IN ('tierr-retry-storm')
  AND (f.offered IS NULL OR f.offered <> f.retries + f.refused OR f.e_offered <> f.offered OR f.e_asked <> f.retries
       OR f.e_retried <> f.e_ended + f.e_pending OR f.e_pending <> 0 OR f.refused_budget <> f.exhaustions
       OR (f.deposits - f.withdrawals - f.expired <> f.level)
       OR f.forbidden <> 0))
UNION ALL
SELECT 'no run of tierr-retry-storm' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'tierr-retry-storm');
