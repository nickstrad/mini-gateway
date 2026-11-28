-- Run with: duckdb -bail -c "SET VARIABLE e4_root='<campaign>';" -f this-file
-- Independent of the report's verdict rules: inspector errors must not count clean.
CREATE TEMP VIEW e4 AS
WITH cfg AS (
 SELECT regexp_replace(filename, '/[^/]+$', '') AS dir,
 max(value) FILTER (WHERE key='release.policy') AS policy,
 max(value) FILTER (WHERE key='reset.reseed') AS reseed
 FROM read_csv(getvariable('e4_root') || '/*/lab.csv', filename=true, all_varchar=true)
 GROUP BY dir
)
SELECT o.*, c.policy, c.reseed, c.dir
FROM read_csv(getvariable('e4_root') || '/*/oracle.csv', filename=true, union_by_name=true) o
JOIN cfg c ON c.dir=regexp_replace(o.filename, '/[^/]+$', '');
SELECT CASE WHEN count(*)=0 THEN error('empty oracle input') ELSE 'oracle input present' END FROM e4;
SELECT CASE WHEN count(*)>0 THEN error('inspector error or unchecked catalog') ELSE 'checker rows complete' END
FROM e4 WHERE contains(note, 'ERROR=') OR least(prepared,cursors,listens,advisory_locks,settings_changed,temp_relations,role_changed)<0;
SELECT CASE WHEN count(*)>0 THEN error('forbidden state or secret crossed lease boundary') ELSE 'isolation expectations held' END
FROM e4 WHERE policy<>'none' AND (policy IN ('reset-always','remake-always') OR prev_tenant<>tenant)
AND greatest(prepared,cursors,listens,advisory_locks,settings_changed,temp_relations,role_changed,canary_read)>0;
SELECT CASE WHEN count(*)>0 THEN error('remake backend reused') ELSE 'backend identity expectations held' END
FROM (SELECT dir,policy,backend_pid,backend_start_ns,count(*) n,count(DISTINCT tenant) tenants FROM e4 GROUP BY ALL)
WHERE (policy='remake-always' AND n>1) OR (policy='remake-on-switch' AND tenants>1);
SELECT CASE WHEN count(*)>0 THEN error('unexpected residue result') ELSE 'residue expectations held' END
FROM e4 WHERE (policy IN ('remake-always','remake-on-switch') AND (policy='remake-always' OR prev_tenant<>tenant)
AND greatest(probe_seed_inherited,probe_custom_name)>0)
OR (policy IN ('reset-always','reset-on-switch') AND (policy='reset-always' OR prev_tenant<>tenant)
AND probe_seed_inherited>=0 AND probe_seed_inherited<>CASE WHEN reseed='on' THEN 0 ELSE 1 END
AND prev_tenant>=0);
SELECT policy,reseed,count(*) nrows,count(*) FILTER (WHERE contains(note,'scope=cross_tenant')) cross_tenant,
 count(*) FILTER (WHERE canary_read=1 AND contains(note,'allowed=true')) allowed_reads,
 count(*) FILTER (WHERE canary_read=1 AND NOT contains(note,'allowed=true')) other_reads,
 sum(probe_seed_inherited=1) seed_hits,sum(probe_custom_name=1) custom_names
FROM e4 GROUP BY ALL ORDER BY policy,reseed;

SELECT CASE WHEN least(max(prepared),max(cursors),max(listens),max(advisory_locks),max(settings_changed),max(temp_relations),max(role_changed))>0
 THEN 'baseline exercises every catalog column' ELSE error('baseline catalog has no teeth') END FROM e4 WHERE policy='none';
WITH required(kind) AS (VALUES ('set'),('set_config_fn'),('hidden'),('prepare'),('cursor_hold'),('listen'),('advisory_lock'),('temp_table'),('set_role'),('custom_setting'),('all'),('error_after'),('local_commit'),('session_auth'),('multi'),('many_prepare'),('temp_sequence'))
SELECT CASE WHEN count(*)>0 THEN error('baseline canary missing') ELSE 'baseline exercises every secret-bearing kind' END
FROM required r WHERE NOT EXISTS (SELECT 1 FROM e4 o WHERE o.policy='none' AND o.canary_kind=r.kind AND o.canary_read=1 AND o.prev_tenant<>o.tenant);
