#!/bin/bash
# Chunk 12 part of scripts/gen-docs.sh: docs/fault-matrix.md, generated from internal/proxy/matrix.go
# by the test TestFaultMatrixDocumentIsGeneratedFromTheCode. With --check the committed file must
# equal the output.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd -P)
cd "$root"
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
test='^TestFaultMatrixDocumentIsGeneratedFromTheCode$'
if [ "${1:-}" = "--check" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  go test -count=1 -run "$test" ./internal/proxy -fault-matrix-out "$tmp" >"$tmp/log" 2>&1 || { cat "$tmp/log"; exit 1; }
  if ! diff -u docs/fault-matrix.md "$tmp/fault-matrix.md"; then
    echo "gen-docs: docs/fault-matrix.md is stale; run scripts/gen-docs.sh" >&2
    exit 1
  fi
  echo "NOTE: docs/fault-matrix.md is current"
else
  go test -count=1 -run "$test" ./internal/proxy -update-fault-matrix >/dev/null
fi
