# Lease-lab report tables

Campaign: `/root/runs/lease-r/2-r8`. SQL: `/root/Software/mini-gateway-work/lease-lab/test/analysis/lease/report`. Calibration: `/root/Software/mini-gateway-work/lease-lab/agent_work/chunks/c05-classifier/lease-exp/calibration.json`.
Parameters: `server_extra=0 sim_real_band_pct=100`.

| Table | File | Rows | What it prints |
| --- | --- | ---: | --- |
| 00-validity | [00-validity.csv](00-validity.csv), [00-validity.md](00-validity.md) | 6 | Table 0: conservation and validity. RUN FIRST. Any row = the campaign is not readable as is. |
| 01-calibration | [01-calibration.csv](01-calibration.csv), [01-calibration.md](01-calibration.md) | 45 | Table 1: calibration — what a reset, a connect, a close and a first statement cost here. |
| 02-isolation | [02-isolation.csv](02-isolation.csv), [02-isolation.md](02-isolation.md) | 0 | Table 2: isolation matrix — policy x state kind -> leaks found. |
| 03-cost | [03-cost.csv](03-cost.csv), [03-cost.md](03-cost.md) | 54 | Table 3: cost per lease — reset against remake, quantiles. |
| 04-capacity | [04-capacity.csv](04-capacity.csv), [04-capacity.md](04-capacity.md) | 27 | Table 4: capacity — goodput against offered load per policy, ratio to the baseline on the same seed, predicted knee beside the measured one. |
| 05-waiting | [05-waiting.csv](05-waiting.csv), [05-waiting.md](05-waiting.md) | 54 | Table 5: waiting — lease_wait_ns and switch_ready_ns quantiles per policy and load, the baseline beside each, ratio; refusals beside every quantile (a refused lease is in no histogram). |
| 06-warm-stock | [06-warm-stock.csv](06-warm-stock.csv), [06-warm-stock.md](06-warm-stock.md) | 9 | Table 6: warm stock — miss rate against W, measured beside P(Poisson(m) >= W), with a binomial band. |
| 07-rule | [07-rule.csv](07-rule.csv), [07-rule.md](07-rule.md) | 24 | Table 7: the rule — R1 (warm.mode=dynamic) against each fixed W and against hindsight-fixed. |
| 07b-rule-step | [07b-rule-step.csv](07b-rule-step.csv), [07b-rule-step.md](07b-rule-step.md) | 0 | Table 7b: the warm target against the offered load over time (profile `step`; table 7's second part). |
| 08-sim-vs-real | [08-sim-vs-real.csv](08-sim-vs-real.csv), [08-sim-vs-real.md](08-sim-vs-real.md) | 30 | Table 8: simulator against real — the paired runs' hit rate, wait quantiles and goodput. |
| 09-verdict | [09-verdict.csv](09-verdict.csv), [09-verdict.md](09-verdict.md) | 45 | Table 9: verdict inputs against plan section 10 — one row per arm and threshold: the measured value, the threshold, and pass / fail / inconclusive. The SQL computes; it does not editorialise. |
| 10-switch-budget | [10-switch-budget.csv](10-switch-budget.csv), [10-switch-budget.md](10-switch-budget.md) | 27 | Table 10: switch-rate budget (Nick's ruling of 2026-11-25) — tenant switches per second as a fraction of the connect ceiling, and the CPU that rate costs. |

**NOT READABLE:** table 00 has 6 rows; read it before any other table.
