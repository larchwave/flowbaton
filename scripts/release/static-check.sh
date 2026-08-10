#!/usr/bin/env bash
set -euo pipefail

workflow=.github/workflows/release-publish.yml
for required in \
  'scripts/release/verify-tag.sh' \
  'scripts/ci/android-connected.sh' \
  'scripts/ci/ios-simulator-test.sh' \
  'FLOWBATON_WEB_LIVE=1' \
  'driver-manifest.json' \
  'actions/attest-build-provenance@' \
  'scripts/release/smoke-posix.sh' \
  'scripts/release/smoke-windows.ps1' \
  'HOMEBREW_TAP_TOKEN'
do
  grep -Fq "$required" "$workflow" || { echo "release workflow is missing: $required" >&2; exit 1; }
done

grep -Fq 'connectedDebugAndroidTest' scripts/ci/android-connected.sh || { echo 'Android release gate does not execute instrumentation' >&2; exit 1; }
grep -Fq 'testTheAutomationCanSeeTheDevice' scripts/ci/ios-simulator-test.sh || { echo 'iOS release gate does not execute the Simulator integration test' >&2; exit 1; }

for installer in scripts/install.sh scripts/install.ps1; do
  grep -Fq 'gh attestation verify' "$installer" || { echo "$installer does not verify GitHub attestations" >&2; exit 1; }
  grep -Fq 'larchwave/flowbaton' "$installer" || { echo "$installer is not repository-bound" >&2; exit 1; }
  grep -Fq '.github/workflows/release-publish.yml' "$installer" || { echo "$installer is not workflow-bound" >&2; exit 1; }
done

if grep -Fq 'com.apple.quarantine' .goreleaser.yaml; then
  echo 'Homebrew cask must not bypass Gatekeeper quarantine' >&2
  exit 1
fi
