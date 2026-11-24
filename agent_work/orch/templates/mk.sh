#!/bin/bash
# Fill a brief template. Usage: mk.sh <template> <out-file> KEY=VALUE...
# Every @KEY@ in the template is replaced; unset keys are replaced by nothing after a warning.
set -eu
t=$(dirname "$0")/$1.md; out=$2; shift 2
s=$(cat "$t")
for kv in "$@"; do k=${kv%%=*}; v=${kv#*=}; s=${s//@$k@/$v}; done
left=$(grep -o '@[A-Z_]*@' <<<"$s" | sort -u | tr '\n' ' ' || true)
[ -z "$left" ] || { echo "mk.sh: unset keys: $left" >&2; for k in $left; do s=${s//$k/}; done; }
printf '%s\n' "$s" > "$out"; echo "wrote $out"
