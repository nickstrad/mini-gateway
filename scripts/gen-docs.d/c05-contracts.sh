#!/bin/bash
# Chunk 5 part of scripts/gen-docs.sh: docs/error-contract.md and docs/reject-list.md,
# generated from internal/pgwire (errors.go, rejectlist.go) by the test
# TestClassifierWriteContractDocs. With --check the committed files must equal the output.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd -P)
cd "$root"
command -v go >/dev/null 2>&1 || PATH="/usr/local/go/bin:$PATH"
check=0
case "${1:-}" in
  --check) check=1 ;;
  "") ;;
  *) echo "c05-contracts.sh: unknown argument $1" >&2; exit 2 ;;
esac
out=$(mktemp -d /tmp/gen-docs-c05.XXXXXX)
trap 'rm -rf "$out"' EXIT
MG_GEN_DOCS_DIR="$out" go test -count=1 -run '^TestClassifierWriteContractDocs$' ./internal/pgwire >/dev/null
rc=0
for f in error-contract.md reject-list.md; do
  [ -s "$out/$f" ] || { echo "c05-contracts.sh: $f was not generated" >&2; exit 1; }
  if [ "$check" = 1 ]; then
    if ! cmp -s "$out/$f" "docs/$f"; then
      echo "docs/$f differs from the code; run scripts/gen-docs.sh" >&2
      diff -u "docs/$f" "$out/$f" | head -n 40 >&2 || true
      rc=1
    fi
  else
    cp "$out/$f" "docs/$f"
  fi
done
exit $rc
