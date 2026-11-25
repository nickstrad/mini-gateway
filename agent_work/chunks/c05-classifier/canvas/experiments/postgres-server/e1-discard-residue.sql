\set ON_ERROR_STOP off
CREATE ROLE tenant_a LOGIN; CREATE ROLE tenant_b; GRANT tenant_b TO tenant_a;
CREATE SEQUENCE s; 
\c - tenant_a
\echo == before
SELECT current_user, session_user, current_setting('myapp.tenant', true) AS placeholder;
SET ROLE tenant_b;
SET myapp.tenant = 'A';
SELECT nextval('s');   -- needs privilege; expect error as tenant_b
LOAD 'auto_explain';
SET auto_explain.log_min_duration = 0;
SELECT pg_advisory_lock(1);
SELECT pg_advisory_xact_lock(2);
CREATE TEMP TABLE t(x int); INSERT INTO t VALUES (1);
PREPARE p AS SELECT 1; LISTEN c;
BEGIN; DECLARE cur CURSOR WITH HOLD FOR SELECT 1; COMMIT;
SELECT pg_my_temp_schema()::regnamespace AS tempns;
DISCARD ALL;
\echo == after DISCARD ALL
SELECT current_user, session_user, current_setting('myapp.tenant', true) AS placeholder, current_setting('auto_explain.log_min_duration', true) AS ae;
SELECT pg_my_temp_schema()::regnamespace AS tempns_after;
SELECT count(*) AS prepared FROM pg_prepared_statements; SELECT count(*) AS cursors FROM pg_cursors;
SELECT pg_listening_channels(); SELECT count(*) FILTER (WHERE locktype='advisory') AS adv FROM pg_locks WHERE pid=pg_backend_pid();
SELECT name, setting, source FROM pg_settings WHERE source NOT IN ('default','override','configuration file','environment variable','client') ;
SELECT * FROM pg_settings WHERE name like 'auto_explain%' LIMIT 1;
