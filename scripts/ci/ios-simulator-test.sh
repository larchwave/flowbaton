#!/usr/bin/env bash
set -euo pipefail

project="${1:-drivers/ios/FlowBatonIOSRunner.xcodeproj}"
derived="${2:-${RUNNER_TEMP:-/tmp}/flowbaton-ios-test}"
udid="$(xcrun simctl list devices available -j | python3 -c '
import json,sys
data=json.load(sys.stdin)
for runtime in sorted(data["devices"], reverse=True):
    for device in data["devices"][runtime]:
        if device.get("isAvailable") and "iPhone" in device.get("name", ""):
            print(device["udid"])
            raise SystemExit
raise SystemExit("no available iPhone Simulator")
')"

xcrun simctl boot "$udid" 2>/dev/null || true
xcrun simctl bootstatus "$udid" -b
xcodebuild -quiet \
  -project "$project" \
  -scheme FlowBatonIOSRunnerUITests \
  -configuration Debug \
  -destination "platform=iOS Simulator,id=${udid}" \
  -derivedDataPath "$derived" \
  COMPILER_INDEX_STORE_ENABLE=NO \
  test \
  -only-testing:FlowBatonIOSRunnerUITests/RunnerHostTests/testTheAutomationCanSeeTheDevice
