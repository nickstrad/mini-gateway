\set ON_ERROR_STOP off
REVOKE EXECUTE ON FUNCTION pg_catalog.set_config(text,text,boolean) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION pg_catalog.pg_advisory_lock(bigint) FROM PUBLIC;
REVOKE TEMPORARY ON DATABASE postgres FROM PUBLIC;
\c - tenant_a
SELECT set_config('search_path','h',false);
SET search_path = h;
SELECT pg_advisory_lock(1);
SELECT pg_advisory_lock(1,2);
CREATE TEMP TABLE x(i int);
SET search_path = pg_temp, public;
PREPARE p AS SELECT 1; LISTEN c;
BEGIN; DECLARE c CURSOR WITH HOLD FOR SELECT 1; COMMIT;
UPDATE pg_settings SET setting = 'q' WHERE name = 'search_path';
SHOW search_path;
\c - postgres
SELECT proname, proacl FROM pg_proc WHERE proname IN ('set_config','pg_advisory_lock') ;
SELECT classid::regclass, objoid::regprocedure, privtype FROM pg_init_privs WHERE objoid = 'pg_catalog.set_config(text,text,boolean)'::regprocedure;
GRANT EXECUTE ON FUNCTION pg_catalog.set_config(text,text,boolean) TO PUBLIC;
GRANT EXECUTE ON FUNCTION pg_catalog.pg_advisory_lock(bigint) TO PUBLIC;
GRANT TEMPORARY ON DATABASE postgres TO PUBLIC;
