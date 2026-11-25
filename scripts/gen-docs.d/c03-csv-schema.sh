#!/bin/bash
# Generates docs/csv-schema.md (the CSV flush schema and the metric registry) from
# internal/obs/catalog, through that package's test (it has no main package: internal/obs/...
# holds no process code). With --check, generates into a scratch folder and diffs.
set -eu
cd "$(dirname "$0")/../.."
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
doc=docs/csv-schema.md
test='^TestCSVSchemaDocumentIsGeneratedFromTheCode$'
if [ "${1:-}" = "--check" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  go test -count=1 -run "$test" ./internal/obs/catalog -out "$tmp/csv-schema.md" >"$tmp/log" 2>&1 || { cat "$tmp/log"; exit 1; }
  if ! diff -u "$doc" "$tmp/csv-schema.md"; then
    echo "gen-docs: $doc is stale; run scripts/gen-docs.sh" >&2
    exit 1
  fi
  echo "NOTE: $doc is current"
else
  go test -count=1 -run "$test" ./internal/obs/catalog -update >/dev/null
fi
