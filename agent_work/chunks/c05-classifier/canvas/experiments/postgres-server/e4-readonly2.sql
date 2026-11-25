\set ON_ERROR_STOP off
CREATE TEMP TABLE rt(x int); CREATE TEMP SEQUENCE rts;
CREATE OR REPLACE FUNCTION f_set2() RETURNS int LANGUAGE plpgsql AS $$BEGIN PERFORM set_config('search_path','h',false); RETURN 1; END$$;
BEGIN READ ONLY;
INSERT INTO rt VALUES (1);
SELECT nextval('rts');
DECLARE hc CURSOR WITH HOLD FOR SELECT 1;
SELECT f_set2();
SELECT pg_notify('c1','x');
SELECT lo_create(0);
SELECT pg_advisory_lock_shared(43);
COMMIT;
SHOW search_path;
SELECT name FROM pg_cursors;
