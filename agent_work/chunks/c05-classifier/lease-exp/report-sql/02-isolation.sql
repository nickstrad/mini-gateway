-- Table 2: isolation matrix — policy x state kind -> leaks found.
-- Reads: oracle.csv (and leases.csv where present) of every readable run (views.sql `iso`).
-- Keys: run.tier, run.campaign, run.test, release.policy, remake.patience (remake-on-switch
--   only), reset.reseed; kind; class (state, canary, structural, identity, residue).
-- Formulas: checked = oracle rows whose column is >= 0 (-1 = not checked); found = rows with
--   a value > 0 on a lease the policy promises clean (every lease under reset-always and
--   remake-always; a change of tenant otherwise); same_tenant = hits on a same-tenant lease
--   (reset-on-switch's documented property, not a leak). structural_reset (S1): a lease after
--   another on one connection with no completed reset where the policy needs one, or any
--   cross-tenant (remake-on-switch) / any (remake-always) sharing. backend_identity (R3):
--   backends (pid, start) seen with two tenants (remake-on-switch) or two leases
--   (remake-always); connection_identity: the same by pool connection from leases.csv.
--   Residue (R4) rows are listed apart, never counted as leaks. status `not checked` when no
--   row was checked.
SELECT * FROM (
SELECT r.tier, r.campaign, r.test, r.policy, r.patience_label AS patience, r.reseed, i.kind, i.class,
  count(DISTINCT i.dir) AS runs, sum(i.checked) AS checked, sum(i.found) AS found, sum(i.same_tenant) AS same_tenant,
  CASE WHEN sum(i.checked) = 0 THEN 'not checked' WHEN sum(i.found) > 0 THEN 'found' ELSE 'none found' END AS status
FROM iso i JOIN ok_runs r USING (dir)
GROUP BY ALL)
ORDER BY tier, campaign, test, policy, patience, reseed, class = 'residue', class, kind;
