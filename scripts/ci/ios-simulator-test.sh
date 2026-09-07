#!/usr/bin/env bash
set -euo pipefail

project="${1:-drivers/ios/FlowBatonIOSRunner.xcodeproj}"
derived="${2:-${RUNNER_TEMP:-/tmp}/flowbaton-ios-test}"
here="$(cd "$(dirname "$0")" && pwd)"
udid="$("$here/ios-simulator-boot.sh")"
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

# The settle fixture (drivers/ios/UITests/SettleFixture) is a test subject with
# its own scheme and its own derived directory: the release packager archives
# the runner's whole Products directory, so the fixture must never land there.
fixture_derived="${derived}-fixture"
xcodebuild -quiet \
  -project "$project" \
  -scheme FlowBatonSettleFixture \
  -configuration Debug \
  -destination "platform=iOS Simulator,id=${udid}" \
  -derivedDataPath "$fixture_derived" \
  CODE_SIGNING_ALLOWED=NO \
  COMPILER_INDEX_STORE_ENABLE=NO \
  build
fixture_app="$(find "$fixture_derived/Build/Products" -maxdepth 2 -name 'FlowBatonSettleFixture.app' -type d -print -quit)"
test -n "$fixture_app"
xcrun simctl install "$udid" "$fixture_app"

# TEST_RUNNER_ variables reach the test process; the test skips without it
# because XCUITest cannot probe for an installed app without failing.
TEST_RUNNER_FLOWBATON_SETTLE_FIXTURE_INSTALLED=1 xcodebuild -quiet \
  -xctestrun "$xctestrun" \
  -destination "platform=iOS Simulator,id=${udid}" \
  -derivedDataPath "$derived" \
  test-without-building \
  -only-testing:FlowBatonIOSRunnerUITests/RunnerHostTests/testAHiddenAnimationDoesNotHideAStableHierarchy \
  -only-testing:FlowBatonIOSRunnerUITests/RunnerHostTests/testAScrollDragDoesNotActivateTheLinkBeneathIt \
  -only-testing:FlowBatonIOSRunnerUITests/RunnerHostTests/testALandscapeRotationRotatesThePointsAndTheTaps
