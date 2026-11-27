#!/bin/bash
# Chunk 6a extras for the landing gate (G10): the load tool's canary and its open-loop,
# shortfall and conservation tests; the long (non -short) tests that build binaries (the
# config-check differential against the gateway binary, the cross-process repeatability of
# the load tool); the mgctl model in tier S; the story report's golden and planted-failure
# tests and the concise-mode script tests with DuckDB required.
set -u
set -o pipefail
cd "$(dirname "$0")/../../../.." || exit 2
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
fail=0
run() { echo "extra: $*"; "$@" || fail=1; }
run go test -count=1 ./internal/loadgen/... ./cmd/loadgen ./internal/mgctl ./cmd/mgctl
run go test -count=1 -run 'Mgctl' ./test/e2e
run env MG_REQUIRE_DUCKDB=1 go test -count=1 ./test/analysis/report/ ./scripts/
exit $fail
