-- requires: workload.requests workload.intended admission.tenant_time_at_share
-- claim: fair-many-tenants: per quiet tenant, read from the flushed files (more tenants than the Prometheus label bound of 32): each of the 40 quiet tenants completed with the flooder exactly as many requests as in the run without it (a join of two arms of one seed), all it intended, with no error; and in the flooded arm each quiet tenant spent time at its share, which it reaches only while another tenant is active beside it.
SELECT run_id || ': schema_version ' || schema_version AS violation FROM runs WHERE schema_version <> 1
UNION ALL
(WITH q AS MATERIALIZED (
    SELECT r.seed, r.arm, f.tenant,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'requests' AND f.outcome = 'ok'), 0) AS ok,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'requests' AND f.outcome <> 'ok'), 0) AS failed,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'workload' AND f.metric = 'intended'), 0) AS intended,
           coalesce(sum(f.value) FILTER (WHERE f.component = 'admission' AND f.metric = 'tenant_time_at_share'), 0) AS at_share
    FROM runs r JOIN final f USING (run_id)
    WHERE r.scenario = 'fair-many-tenants' AND f.component IN ('workload', 'admission') AND f.tenant <> '' AND f.tenant NOT LIKE 'tenant0%'
    GROUP BY r.seed, r.arm, f.tenant
), checked AS MATERIALIZED (
    SELECT a.seed, a.tenant, a.ok, a.intended, a.failed, f.ok AS f_ok, f.failed AS f_failed, f.at_share AS f_at_share
    FROM q a LEFT JOIN q f ON f.seed = a.seed AND f.tenant = a.tenant AND f.arm = 'fair' WHERE a.arm = 'alone' AND a.intended > 0
)
SELECT 'seed ' || seed || ', ' || tenant || ': alone ' || ok || ' ok of ' || intended || ' (' || failed || ' failed), with the flood ' || coalesce(f_ok, -1)
       || ' ok (' || coalesce(f_failed, -1) || ' failed), time at share ' || coalesce(f_at_share, -1)
FROM checked WHERE ok < 1 OR ok <> intended OR failed <> 0 OR f_ok IS NULL OR f_ok <> ok OR f_failed <> 0 OR f_at_share < 1
UNION ALL
SELECT 'seed ' || seed || ': only ' || count(*) || ' quiet tenants checked; the scenario must exceed the label bound of 32'
FROM checked GROUP BY seed HAVING count(*) <= 32)
UNION ALL
SELECT 'no alone run of fair-many-tenants' WHERE NOT EXISTS (SELECT 1 FROM runs WHERE scenario = 'fair-many-tenants' AND arm = 'alone');
