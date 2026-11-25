\set ON_ERROR_STOP off
CREATE TABLE IF NOT EXISTS t(x int);
CREATE OR REPLACE FUNCTION liar_set() RETURNS int IMMUTABLE LANGUAGE plpgsql AS $$BEGIN PERFORM set_config('search_path','liar',false); PERFORM pg_advisory_lock(77); RETURN 1; END$$;
CREATE OR REPLACE FUNCTION liar_write() RETURNS int IMMUTABLE LANGUAGE plpgsql AS $$BEGIN INSERT INTO t VALUES (5); RETURN 1; END$$;
CREATE OR REPLACE FUNCTION liar_utility() RETURNS int STABLE LANGUAGE plpgsql AS $$BEGIN SET search_path = s; RETURN 1; END$$;
CREATE OR REPLACE FUNCTION liar_random() RETURNS float8 IMMUTABLE LANGUAGE sql AS $$SELECT random()$$;
SELECT liar_set(); SHOW search_path;
SELECT count(*) FILTER (WHERE locktype='advisory') FROM pg_locks WHERE pid=pg_backend_pid();
SELECT liar_write();
SELECT liar_utility();
SELECT liar_random() a, liar_random() b;
SELECT provolatile, count(*) FROM pg_proc WHERE pronamespace='pg_catalog'::regnamespace GROUP BY 1;
SELECT proname, provolatile, proparallel FROM pg_proc WHERE proname IN ('set_config','pg_advisory_lock','nextval','now','random','pg_notify','txid_current','current_setting') ORDER BY 1;
CREATE OR REPLACE VIEW v AS SELECT random() r;
EXPLAIN (VERBOSE, FORMAT JSON) SELECT * FROM v;
