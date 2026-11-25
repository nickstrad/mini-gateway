#!/bin/bash
# Generates docs/csv-schema.md (the CSV flush schema and the metric registry) from
# internal/obs/catalog. With --check, generates into a scratch folder and diffs.
set -eu
cd "$(dirname "$0")/../.."
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
doc=docs/csv-schema.md
if [ "${1:-}" = "--check" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  go run ./internal/obs/catalog/csvschema "$tmp/csv-schema.md"
  if ! diff -u "$doc" "$tmp/csv-schema.md"; then
    echo "gen-docs: $doc is stale; run scripts/gen-docs.sh" >&2
    exit 1
  fi
  echo "NOTE: $doc is current"
else
  go run ./internal/obs/catalog/csvschema "$doc"
fi
