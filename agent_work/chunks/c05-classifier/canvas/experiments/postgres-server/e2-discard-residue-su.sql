\set ON_ERROR_STOP off
\c - tenant_a
SELECT current_setting('myapp.tenant', true) IS NULL AS placeholder_null_fresh;
SET myapp.tenant = 'A';
DISCARD ALL;
SELECT current_setting('myapp.tenant', true) IS NULL AS placeholder_null_after, quote_literal(current_setting('myapp.tenant', true)) AS val;
-- seed / PRNG
SELECT setseed(0.5); SELECT random() AS r_after_setseed;
SELECT setseed(0.5); DISCARD ALL; SELECT random() AS r_after_setseed_then_discard;
\c - postgres
LOAD 'auto_explain'; SET auto_explain.log_min_duration = 0;
SELECT pg_replication_origin_create('o1');
SELECT pg_replication_origin_session_setup('o1');
CREATE TEMP SEQUENCE ts; SELECT nextval('ts');
CREATE SEQUENCE IF NOT EXISTS s2; SELECT nextval('s2');
SET SESSION AUTHORIZATION tenant_a;
DISCARD ALL;
SELECT session_user, current_user;
SELECT current_setting('auto_explain.log_min_duration', true) AS ae_after, (SELECT count(*) FROM pg_settings WHERE name LIKE 'auto_explain%') AS ae_gucs_visible;
SELECT pg_replication_origin_session_is_setup() AS origin_still_setup;
SELECT lastval();
SELECT currval('s2');
SELECT pg_replication_origin_session_reset(); SELECT pg_replication_origin_drop('o1');
