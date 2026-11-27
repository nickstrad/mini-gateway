# mini-gateway: the commands a builder runs. Each target is one script; read the script's
# header for what it does.
#
#   make test             the CI set (scripts/ci-fast.sh)
#   make chaos            the named chaos scenarios, tier S then tier R (scripts/chaos.sh;
#                         CHAOS_SEEDS=N, VERBOSE=1 for the raw output)
#   make bench            the benchmarks                 (not built yet: chunk 14)
#   make gate CHUNK=c01-sim-kernel    the local landing gate for one chunk
#   make ci               scripts/ci-fast.sh, exactly what CI runs
#   make lint             the four lints only (gate check G1)
#   make site             build the Pages site into _site/
#   make csv-assert RUNS=<dir> [SQL=<files>]   SQL assertions over flushed CSV runs (DuckDB)
#
# test, chaos and bench fail with "not built yet" while their marker file exists under
# scripts/.not-built/, so a green target is never a stub. The chunk that builds a target
# deletes its marker in the same commit.

SHELL := /bin/bash
NOT_BUILT := scripts/.not-built

.PHONY: test chaos bench gate ci lint site csv-assert

test:
	@if [ -e $(NOT_BUILT)/make-test ]; then echo "make test: not built yet" >&2; exit 1; fi
	scripts/ci-fast.sh

chaos:
	@scripts/chaos.sh

bench:
	@scripts/bench.sh

gate:
	@[ -n "$(CHUNK)" ] || { echo "usage: make gate CHUNK=<chunk-dir>   (a folder name under agent_work/chunks/)" >&2; exit 2; }
	scripts/gate.sh $(CHUNK)

ci:
	scripts/ci-fast.sh

lint:
	@GATE_ONLY=G1 GATE_OUT=agent_work/ci/lint scripts/gate.sh lint

site:
	scripts/site.sh _site

csv-assert:
	@[ -n "$(RUNS)" ] || { echo "usage: make csv-assert RUNS=<root or run directory> [SQL=<sql files>]" >&2; exit 2; }
	scripts/csv-assert.sh $(RUNS) $(SQL)

# The story report (scripts/report.sh, PLAN.md section 19.3 items 1 and 2):
#   make report RUNS=<dir...>   report over flushed CSV runs (run directories or roots)
#   make report SEEDS=N         run the tier S e2e scenarios at N seeds into a new /tmp folder
#                               (the go test output goes to its e2e.log), then report over it
.PHONY: report
report:
	@if [ -n "$(RUNS)" ]; then scripts/report.sh $(RUNS); \
	elif [ -n "$(SEEDS)" ]; then \
	  command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$$PATH"; \
	  d=$$(mktemp -d /tmp/report.XXXXXX) || exit 2; rc=0; \
	  echo "report: tier S e2e scenarios at $(SEEDS) seeds into $$d/runs (log $$d/e2e.log)"; \
	  DST_SEEDS=$(SEEDS) RUN_DIR="$$d/runs" go test -count=1 -run '^TestScenarioE2E$$' ./test/e2e >"$$d/e2e.log" 2>&1 || rc=$$?; \
	  if [ $$rc != 0 ]; then \
	    echo "report: go test failed (exit $$rc); its failures:"; \
	    grep -E -- '--- FAIL|REPLAY: ' "$$d/e2e.log" | sed 's/^[[:space:]]*/  /'; tail -n 5 "$$d/e2e.log" | sed 's/^/  /'; \
	  fi; \
	  scripts/report.sh "$$d/runs" || exit $$?; exit $$rc; \
	else echo "usage: make report RUNS=<run directory or root...> | make report SEEDS=<n>" >&2; exit 2; fi

# The run targets (deploy/dev/, PLAN.md chunk 6a and section 19.3 item 3): a gateway, a
# Postgres 17 and a Prometheus scraping the gateway, a constant load, and the way down.
#   make run-up                                   build, start the stack, wait for the scrape, print URLs
#   make load RATE=100 DURATION=30s PROFILE=simple|errors SEED=<n>   open-loop load (cmd/loadgen;
#                                                 also CONNS=8, LOAD_ARGS=<more loadgen flags>)
#   make run-down                                 stop the gateway, remove containers, network, volumes
# Settings (ports, state directory): deploy/dev/env.sh.
.PHONY: run-up load run-down
run-up:
	@deploy/dev/up.sh

load:
	@RATE="$(RATE)" DURATION="$(DURATION)" PROFILE="$(PROFILE)" SEED="$(SEED)" CONNS="$(CONNS)" LOAD_ARGS="$(LOAD_ARGS)" deploy/dev/load.sh

run-down:
	@deploy/dev/down.sh
