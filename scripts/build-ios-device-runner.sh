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
#
# By default xcodebuild may ask the Apple Developer portal to create or update
# profiles, App IDs and certificates. Set FLOWBATON_IOS_LOCAL_SIGNING=1 to sign
# with the assets already installed in Xcode and never touch the portal; a
# missing profile then fails the build with xcodebuild's own message instead
# of being created (issue #22).
set -euo pipefail

if [[ -z "${FLOWBATON_IOS_TEAM:-}" ]]; then
  echo "set FLOWBATON_IOS_TEAM to your Apple Developer team id (Xcode > Settings > Accounts)" >&2
  exit 1
fi

repo="$(cd "$(dirname "$0")/.." && pwd)"
derived="${FLOWBATON_IOS_DERIVED_DATA:-$HOME/.flowbaton/ios-driver}"

xcodegen generate --spec "$repo/drivers/ios/project.yml" --project "$repo/drivers/ios"

signing=()
if [[ -n "${FLOWBATON_IOS_LOCAL_SIGNING:-}" ]]; then
  echo "local signing only: building from the installed profiles and certificates, not asking the portal" >&2
else
  signing=(-allowProvisioningUpdates)
fi

xcodebuild -quiet \
  -project "$repo/drivers/ios/FlowBatonIOSRunner.xcodeproj" \
  -scheme FlowBatonIOSRunnerUITests \
  -configuration Debug \
  -destination 'generic/platform=iOS' \
  -derivedDataPath "$derived" \
  ${signing[@]+"${signing[@]}"} \
  COMPILER_INDEX_STORE_ENABLE=NO \
  build-for-testing

xctestrun="$(find "$derived/Build/Products" -maxdepth 1 -name '*_iphoneos*.xctestrun' -type f -print -quit)"
if [[ -z "$xctestrun" ]]; then
  echo "no *_iphoneos*.xctestrun under $derived/Build/Products — the build did not produce a device runner" >&2
  exit 1
fi
echo "device runner built: $xctestrun"
echo "run flows with: flowbaton test -p ios --device <udid> <flow.yaml>"
