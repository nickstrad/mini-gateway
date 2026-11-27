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
