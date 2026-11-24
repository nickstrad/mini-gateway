#!/bin/bash
# Build the Pages site locally, book pages included.
#
# Usage: scripts/site.sh [output-dir]        (default: _site)
#
# Runs .github/scripts/build_site.py. That script needs the Python package `markdown`.
# Where it comes from, first match wins:
#   1. the Python already on this machine, if it can import it (CI installs it with pip);
#   2. SITE_PYLIB=<dir>, a folder that holds the unpacked package;
#   3. .cache/pylib in this checkout (gitignored). If it is not there yet the pinned wheel
#      is downloaded from PyPI, checked against its SHA-256 and unzipped there. This is for
#      machines whose Python has no pip, such as vm-1.
#
# Exit status: 0 built, 1 the build failed (missing source, broken link, bad book
# manifest), 2 the markdown package could not be found or fetched.
set -u
set -o pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
out=${1:-_site}
wheel_url=https://files.pythonhosted.org/packages/81/6f/1fa8970e960e5c832874399e6efa564870ade8206d57fef5d5e296b834f5/markdown-3.11.3-py3-none-any.whl
wheel_sha=e1a31a112232504d753e5c01bb8c2de90d3f0ba986b063e10770cf7695b09e73

if ! python3 -c 'import markdown' 2>/dev/null; then
  lib=${SITE_PYLIB:-$root/.cache/pylib}
  if [ ! -f "$lib/markdown/__init__.py" ]; then
    [ -z "${SITE_PYLIB:-}" ] || { echo "site: SITE_PYLIB=$lib has no markdown package" >&2; exit 2; }
    tmp=$(mktemp -d) || exit 2
    trap 'rm -rf "$tmp"' EXIT
    echo "site: fetching the markdown wheel into $lib" >&2
    curl -fsSL -o "$tmp/markdown.whl" "$wheel_url" || { echo "site: cannot download $wheel_url" >&2; exit 2; }
    echo "$wheel_sha  $tmp/markdown.whl" | sha256sum -c --quiet - || { echo "site: the wheel does not match its pinned SHA-256" >&2; exit 2; }
    mkdir -p "$lib" && python3 -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' "$tmp/markdown.whl" "$lib" \
      || { echo "site: cannot unpack the wheel" >&2; exit 2; }
  fi
  export PYTHONPATH="$lib${PYTHONPATH:+:$PYTHONPATH}"
  python3 -c 'import markdown' || { echo "site: the markdown package in $lib does not import" >&2; exit 2; }
fi
case "$out" in /*) ;; *) out="$PWD/$out" ;; esac
python3 "$root/.github/scripts/build_site.py" "$out" || exit 1
