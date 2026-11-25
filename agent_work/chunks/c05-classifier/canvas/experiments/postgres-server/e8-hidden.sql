\set ON_ERROR_STOP off
CREATE OR REPLACE FUNCTION public.h_listen() RETURNS int LANGUAGE plpgsql AS $$BEGIN EXECUTE 'LISTEN hc'; RETURN 1; END$$;
CREATE OR REPLACE FUNCTION public.h_prepare() RETURNS int LANGUAGE plpgsql AS $$BEGIN EXECUTE 'PREPARE hq AS SELECT 1'; RETURN 1; END$$;
CREATE OR REPLACE FUNCTION public.h_set() RETURNS int LANGUAGE plpgsql AS $$BEGIN SET work_mem = '77MB'; RETURN 1; END$$;
CREATE OR REPLACE FUNCTION public.h_temp() RETURNS int LANGUAGE plpgsql AS $$BEGIN CREATE TEMP TABLE IF NOT EXISTS htt(x int); RETURN 1; END$$;
CREATE OR REPLACE FUNCTION public.h_cursor() RETURNS int LANGUAGE plpgsql AS $$BEGIN EXECUTE 'DECLARE hcur CURSOR WITH HOLD FOR SELECT 1'; RETURN 1; END$$;
CREATE OR REPLACE FUNCTION public.h_discard() RETURNS int LANGUAGE plpgsql AS $$BEGIN EXECUTE 'DISCARD ALL'; RETURN 1; END$$;
SELECT public.h_listen(); SELECT public.h_prepare(); SELECT public.h_set(); SELECT public.h_temp(); SELECT public.h_cursor(); SELECT public.h_discard();
SELECT pg_listening_channels(); SELECT name FROM pg_prepared_statements; SHOW work_mem; SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema();
SELECT name, source FROM pg_settings WHERE source = 'session';
