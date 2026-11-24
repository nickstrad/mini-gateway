#!/bin/bash
# Observability chain: the proxy's OTLP push through an OpenTelemetry Collector into Prometheus.
#
# Usage: scripts/obs-chain.sh
# Needs Docker. On demand only (PLAN.md section 7.9).
#
# NOT BUILT YET. This is the chunk 0 skeleton: it prints "not built yet" and exits 3.
# While the marker file scripts/.not-built/obs-chain exists, scripts/gate.sh records this
# stage as skip-not-built. The chunk that writes this script (chunk 15) deletes the marker in the
# same commit; from then on the stage must run and pass, and a script that still exits 3
# is a red gate.
#
# Exit status (once built): 0 green, 1 a check failed, 2 usage or setup error.
echo "obs-chain.sh: not built yet" >&2
exit 3
