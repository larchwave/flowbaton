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
  build-for-testing

xctestrun="$(find "$derived/Build/Products" -maxdepth 1 -name '*.xctestrun' -type f -print -quit)"
test -n "$xctestrun"
# Without -derivedDataPath, xcodebuild mints a fresh hashed directory under
# ~/Library/Developer/Xcode/DerivedData for this run alone and collects a
# simulator sysdiagnose into it -- 120MB of system log archive, written and
# then abandoned. The build above already named a directory; results belong
# beside it.
xcodebuild -quiet \
  -xctestrun "$xctestrun" \
  -destination "platform=iOS Simulator,id=${udid}" \
  -derivedDataPath "$derived" \
  test-without-building \
  -only-testing:FlowBatonIOSRunnerUITests/RunnerHostTests/testTheAutomationCanSeeTheDevice
