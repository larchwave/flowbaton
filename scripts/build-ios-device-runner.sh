#!/usr/bin/env bash
# Builds the FlowBaton iOS runner for PHYSICAL devices and leaves the
# .xctestrun where `flowbaton test -p ios` looks for it
# (~/.flowbaton/ios-driver/Build/Products/*_iphoneos*.xctestrun).
#
# One-time prerequisites:
#   - Xcode with an Apple ID signed in (a free account works; its profiles
#     expire after 7 days and per-team bundle ids must be unique).
#   - export FLOWBATON_IOS_TEAM=<your team id> before running.
#   - The phone connected over USB, unlocked, and trusted.
set -euo pipefail

if [[ -z "${FLOWBATON_IOS_TEAM:-}" ]]; then
  echo "set FLOWBATON_IOS_TEAM to your Apple Developer team id (Xcode > Settings > Accounts)" >&2
  exit 1
fi

repo="$(cd "$(dirname "$0")/.." && pwd)"
derived="${FLOWBATON_IOS_DERIVED_DATA:-$HOME/.flowbaton/ios-driver}"

xcodegen generate --spec "$repo/drivers/ios/project.yml" --project "$repo/drivers/ios"

xcodebuild -quiet \
  -project "$repo/drivers/ios/FlowBatonIOSRunner.xcodeproj" \
  -scheme FlowBatonIOSRunnerUITests \
  -configuration Debug \
  -destination 'generic/platform=iOS' \
  -derivedDataPath "$derived" \
  -allowProvisioningUpdates \
  COMPILER_INDEX_STORE_ENABLE=NO \
  build-for-testing

xctestrun="$(find "$derived/Build/Products" -maxdepth 1 -name '*_iphoneos*.xctestrun' -type f -print -quit)"
if [[ -z "$xctestrun" ]]; then
  echo "no *_iphoneos*.xctestrun under $derived/Build/Products — the build did not produce a device runner" >&2
  exit 1
fi
echo "device runner built: $xctestrun"
echo "run flows with: flowbaton test -p ios --device <udid> <flow.yaml>"
