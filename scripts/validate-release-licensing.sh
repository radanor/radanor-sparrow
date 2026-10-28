#!/usr/bin/env bash
# Release-readiness validation for Radanor Sparrow Community Edition licensing.
#
# This is the gate that must pass before a PUBLIC release, in the default strict
# `release` mode. Development validation uses `--mode development`, which allows
# the documented placeholders (Licensor identity, first-release Change Date,
# pending decisions) but still rejects structural problems. It is not part of a
# plain `go build`, `go test` or `scripts/build-linux.sh`. The explicit release
# path (`scripts/build-linux.sh --release`, `scripts/build-windows.ps1 -Release`)
# and the release workflow call it in strict mode, and it fails closed.
#
# Usage:
#   bash scripts/validate-release-licensing.sh                 # validate metadata
#   bash scripts/validate-release-licensing.sh --release-dir dist/linux
#   bash scripts/validate-release-licensing.sh --mode development # allow documented placeholders
#
# The underlying validator is Go so the same checks run on Linux and Windows.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"
export GOTOOLCHAIN=local
exec go run ./cmd/radanor-release-check "$@"
