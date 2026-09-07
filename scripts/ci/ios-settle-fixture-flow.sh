#!/usr/bin/env bash
# Runs the settle fixture flow through the FlowBaton host on the Simulator.
#
# Usage: ios-settle-fixture-flow.sh <flowbaton-binary> <runner-derived> <output-dir>
#
# <runner-derived> is the derived directory ios-simulator-test.sh built the
# runner into; the fixture app is expected beside it in <runner-derived>-fixture
# and is installed again here so this script also runs on its own. The flow is
# the reporter's shape from issue #7: launch, assert the button, tap it, assert
# the result, all under a decorative animation the hierarchy never shows; then
# issue #12's: scroll a page whose drag start sits on a plain SwiftUI Link.
set -euo pipefail

flowbaton="${1:?flowbaton binary}"
derived="${2:?runner derived directory}"
output="${3:?output directory}"
here="$(cd "$(dirname "$0")" && pwd)"
flow="$here/../../drivers/ios/UITests/SettleFixture/settle-fixture.yaml"

udid="$("$here/ios-simulator-boot.sh")"
xctestrun="$(find "$derived/Build/Products" -maxdepth 1 -name '*.xctestrun' -type f -print -quit)"
test -n "$xctestrun"
fixture_app="$(find "${derived}-fixture/Build/Products" -maxdepth 2 -name 'FlowBatonSettleFixture.app' -type d -print -quit)"
test -n "$fixture_app"
xcrun simctl install "$udid" "$fixture_app"

"$flowbaton" check-syntax "$flow"
FLOWBATON_IOS_XCTESTRUN="$xctestrun" "$flowbaton" test -p ios \
  --device "$udid" \
  --test-output-dir "$output" \
  "$flow"
