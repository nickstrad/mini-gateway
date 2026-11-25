\set ON_ERROR_STOP off
SELECT public.liar_write();
SELECT public.liar_utility();
SELECT public.liar_random() a, public.liar_random() b;
CREATE OR REPLACE VIEW public.v AS SELECT random() r;
EXPLAIN (VERBOSE, FORMAT JSON) SELECT * FROM public.v;
