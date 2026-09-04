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
  'scripts/release/test-packaged-ios-runner.sh' \
  'scripts/release/sign-notarize-darwin.sh' \
  'scripts/release/smoke-homebrew-cask.sh' \
  'scripts/release/anonymous-release-probe.sh' \
  'macos-15-intel' \
  'APPLE_DEVELOPER_ID_CERTIFICATE_BASE64' \
  'APPLE_NOTARY_PRIVATE_KEY_BASE64' \
  'release-signing' \
  'rollback_release_and_tap' \
  'HOMEBREW_TAP_SSH_KEY' \
  'git@github.com:larchwave/homebrew-flowbaton.git' \
  'github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl' \
  'cleanup_tap_ssh'
do
  grep -Fq "$required" "$workflow" || { echo "release workflow is missing: $required" >&2; exit 1; }
done

grep -Fq 'connectedDebugAndroidTest' scripts/ci/android-connected.sh || { echo 'Android release gate does not execute instrumentation' >&2; exit 1; }
grep -Fq 'testTheAutomationCanSeeTheDevice' scripts/ci/ios-simulator-test.sh || { echo 'iOS release gate does not execute the Simulator integration test' >&2; exit 1; }
grep -Fq 'build-for-testing' scripts/ci/ios-simulator-test.sh || { echo 'iOS release gate does not retain the tested products' >&2; exit 1; }
grep -Fq 'test-without-building' scripts/ci/ios-simulator-test.sh || { echo 'iOS release gate does not execute the retained products' >&2; exit 1; }
grep -Fq 'test-without-building' scripts/release/test-packaged-ios-runner.sh || { echo 'packaged iOS runner is not executed' >&2; exit 1; }
grep -Fq 'actual_arches' scripts/release/test-packaged-ios-runner.sh || { echo 'packaged iOS runner architecture is not checked' >&2; exit 1; }

if grep -Fq 'for arch in amd64 arm64' "$workflow"; then
  echo 'one iOS build must not be relabeled for both Darwin architectures' >&2
  exit 1
fi

# Match literal shell source rather than expanding its variables.
# shellcheck disable=SC2016
for required in \
  'codesign --force --options runtime --timestamp' \
  'xcrun notarytool submit' \
  'xcrun notarytool log' \
  'developer_id_intermediate_sha256=7afc9d01a62f03a2de9637936d4afe68090d2de18d03f29c88cfb0b1ba63587f' \
  'security import "$developer_id_intermediate"' \
  "codesign -vvvv -R='notarized' --check-notarization"
do
  grep -Fq "$required" scripts/release/sign-notarize-darwin.sh || { echo "Darwin signing gate is missing: $required" >&2; exit 1; }
done

for required in \
  'brew install --cask' \
  'codesign --verify' \
  "codesign -vvvv -R='notarized' --check-notarization"
do
  grep -Fq "$required" scripts/release/smoke-homebrew-cask.sh || { echo "Homebrew smoke is missing: $required" >&2; exit 1; }
done

for required in \
  'env -i' \
  'anonymous-release-probe.sh' \
  'gh attestation verify' \
  '--signer-workflow' \
  '--source-ref' \
  'sha256sum --check checksums.txt' \
  'public_proof=1'
do
  grep -Fq -- "$required" "$workflow" scripts/release/anonymous-release-probe.sh || { echo "anonymous public proof is missing: $required" >&2; exit 1; }
done

for forbidden in 'HOMEBREW_TAP_TOKEN' 'x-access-token:' 'gh auth setup-git' 'https://github.com/larchwave/homebrew-flowbaton.git' 'StrictHostKeyChecking=no' 'ssh-keyscan'; do
  if grep -Fq "$forbidden" "$workflow" scripts/release/tap-git-ssh.sh; then
    echo "tap publishing contains forbidden credential or SSH behavior: $forbidden" >&2
    exit 1
  fi
done

for required in \
  'GIT_SSH_COMMAND=' \
  'IdentitiesOnly=yes' \
  'UserKnownHostsFile=' \
  'GlobalKnownHostsFile=/dev/null' \
  'StrictHostKeyChecking=yes'
do
  grep -Fq "$required" scripts/release/tap-git-ssh.sh || { echo "tap SSH transport is missing: $required" >&2; exit 1; }
done

scripts/release/test-tap-git-ssh.sh

for credential in HOMEBREW_TAP_TOKEN HOMEBREW_TAP_SSH_KEY FLOWBATON_TAP_SSH_KEY_FILE FLOWBATON_TAP_KNOWN_HOSTS_FILE GIT_SSH_COMMAND; do
  grep -Fq "$credential" scripts/release/anonymous-release-probe.sh || { echo "anonymous probe does not scrub: $credential" >&2; exit 1; }
done

for installer in scripts/install.sh scripts/install.ps1; do
  grep -Fq 'gh attestation verify' "$installer" || { echo "$installer does not verify GitHub attestations" >&2; exit 1; }
  grep -Fq 'larchwave/flowbaton' "$installer" || { echo "$installer is not repository-bound" >&2; exit 1; }
  grep -Fq '.github/workflows/release-publish.yml' "$installer" || { echo "$installer is not workflow-bound" >&2; exit 1; }
done

if grep -Fq 'com.apple.quarantine' .goreleaser.yaml; then
  echo 'Homebrew cask must not bypass Gatekeeper quarantine' >&2
  exit 1
fi

for script in scripts/release/*.sh; do
  bash -n "$script"
done
sh -n scripts/install.sh

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM
PYTHONPYCACHEPREFIX="$tmp/pycache" python3 -m py_compile scripts/release/*.py
printf 'arm candidate' >"$tmp/flowbaton_9.9.9_darwin_arm64.tar.gz"
printf 'intel candidate' >"$tmp/flowbaton_9.9.9_darwin_amd64.tar.gz"
scripts/release/render-homebrew-cask.py \
  --version 9.9.9 --candidate "$tmp" --base-url "file://$tmp" --output "$tmp/flowbaton.rb"
grep -Fq "file://$tmp/flowbaton_9.9.9_darwin_arm64.tar.gz" "$tmp/flowbaton.rb"
grep -Fq "file://$tmp/flowbaton_9.9.9_darwin_amd64.tar.gz" "$tmp/flowbaton.rb"
