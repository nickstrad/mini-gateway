#!/bin/bash
# Chunk 8 gate extra (G10): the admission chunk's own end-to-end list.
# 1. scripts/fuzz-plant.sh: FuzzEngineOps and FuzzAdmissionOps each find their planted ordering bug.
# 2. scripts/admission-head-to-head.sh --check: the head-to-head table committed under
#    test/analysis/admission-head-to-head/ is reproduced exactly by one DuckDB query over freshly
#    flushed runs of the same seed (claim 13).
set -u
root=$(cd "$(dirname "$0")/../../../.." && pwd -P)
cd "$root" || exit 2
scripts/fuzz-plant.sh || exit 1
scripts/admission-head-to-head.sh --check || exit 1
