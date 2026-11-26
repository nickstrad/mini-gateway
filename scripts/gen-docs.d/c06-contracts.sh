#!/bin/bash
# Chunk 6 part of scripts/gen-docs.sh: docs/timeout-budget.md and docs/capacity-table.md,
# generated from internal/proxy/contracts.go, internal/proxy/config.go and
# internal/shell/contracts.go by the test TestContractDocumentsAreGeneratedFromTheCode.
# With --check the committed files must equal the output.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd -P)
cd "$root"
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
test='^TestContractDocumentsAreGeneratedFromTheCode$'
if [ "${1:-}" = "--check" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  go test -count=1 -run "$test" ./internal/shell -contracts-out "$tmp" >"$tmp/log" 2>&1 || { cat "$tmp/log"; exit 1; }
  for doc in timeout-budget.md capacity-table.md; do
    if ! diff -u "docs/$doc" "$tmp/$doc"; then
      echo "gen-docs: docs/$doc is stale; run scripts/gen-docs.sh" >&2
      exit 1
    fi
  done
  echo "NOTE: docs/timeout-budget.md and docs/capacity-table.md are current"
else
  go test -count=1 -run "$test" ./internal/shell -update-contracts >/dev/null
fi
