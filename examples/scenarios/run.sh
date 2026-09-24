#!/usr/bin/env bash
# Run a documented lab only inside a new, disposable Docker/kind cluster.
set -euo pipefail
cd "$(dirname "$0")/../.."
exec python3 examples/scenarios/runner.py "$@"
