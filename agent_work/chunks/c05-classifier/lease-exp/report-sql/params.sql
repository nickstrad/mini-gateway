-- Lease-lab report: the shared parameters. scripts/lease-report.sh reads this file before
-- views.sql (once, to build the report database) and before every table file (variables do
-- not persist in the database file).
--
-- Parameters. Set any of them with `SET VARIABLE <name> = <value>;` BEFORE this file;
-- unset ones take the default written here.
SET VARIABLE root = getvariable('root');                                   -- required: the campaign directory
SET VARIABLE sentinel = coalesce(getvariable('sentinel'), 'test/analysis/lease/report/empty');
SET VARIABLE calibration = coalesce(getvariable('calibration'),
  'agent_work/chunks/c05-classifier/lease-exp/calibration.json');
-- Names not fixed by E2's contract (E5 decides them; change here if E5 chose otherwise):
SET VARIABLE invalid_key = coalesce(getvariable('invalid_key'), 'run.valid');   -- lab.csv key of the runner's validity gate; absent = valid
SET VARIABLE invalid_ok = coalesce(getvariable('invalid_ok'), ['ok', 'valid', '1', 'true', 'yes']);
SET VARIABLE cpu_pg_key = coalesce(getvariable('cpu_pg_key'), 'cpu.pg_ns');     -- lab.csv key: Postgres CPU ns over the run (R9)
SET VARIABLE cpu_lab_key = coalesce(getvariable('cpu_lab_key'), 'cpu.lab_ns');  -- lab.csv key: the lab process's CPU ns
-- cpu.csv, if used instead: columns pg_cpu_ns and lab_cpu_ns (summed over its rows).
-- server.csv: columns time and count (E5 brief); other columns are ignored.
SET VARIABLE server_extra = coalesce(getvariable('server_extra'), 0);  -- backends allowed above the bound below
-- Analysis choices:
SET VARIABLE ceiling_cond = coalesce(getvariable('ceiling_cond'), 'c=16');  -- M7S cell giving the measured connect ceiling (~195/s)
SET VARIABLE band_z = coalesce(getvariable('band_z'), 1.96);           -- binomial band half-width in standard errors (95%)
SET VARIABLE baseline_arm = coalesce(getvariable('baseline_arm'), '');  -- '' = the first arm (by name) with none + cold in its group
SET VARIABLE aa_arm = coalesce(getvariable('aa_arm'), '');              -- '' = the second such arm (the A/A partner)
