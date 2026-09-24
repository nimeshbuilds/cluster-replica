#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
command -v docker >/dev/null
docker info >/dev/null
export REPLICOVE_POSTGRES_INTEGRATION=1
go test ./internal/database -run 'TestPostgreSQLDisposable|TestPreparation|TestMaskFailure|TestInterrupted|TestHostAllow' -count=1 -timeout=5m
