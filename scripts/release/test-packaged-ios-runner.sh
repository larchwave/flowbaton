#!/usr/bin/env bash
set -euo pipefail

archive="${1:?usage: test-packaged-ios-runner.sh ARCHIVE HOST_ARCH}"
host_arch="${2:?usage: test-packaged-ios-runner.sh ARCHIVE HOST_ARCH}"

case "$host_arch" in
  amd64) macho_arch=x86_64 ;;
  arm64) macho_arch=arm64 ;;
  *) echo "unsupported host architecture: $host_arch" >&2; exit 2 ;;
esac

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

tar -xzf "$archive" -C "$tmp"
xctestrun="$(find "$tmp" -name '*.xctestrun' -type f -print -quit)"
runner_app="$(find "$tmp" -name '*Runner.app' -type d -print -quit)"
test -n "$xctestrun"
test -n "$runner_app"

executable_name="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$runner_app/Info.plist")"
runner_binary="$runner_app/$executable_name"
test -x "$runner_binary"
actual_arches="$(xcrun lipo -archs "$runner_binary")"
if [[ "$actual_arches" != "$macho_arch" ]]; then
  echo "packaged iOS runner contains '$actual_arches'; expected exactly '$macho_arch'" >&2
  exit 1
fi
codesign --verify --deep --strict --verbose=2 "$runner_app"

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
  -xctestrun "$xctestrun" \
  -destination "platform=iOS Simulator,id=${udid}" \
  test-without-building \
  -only-testing:FlowBatonIOSRunnerUITests/RunnerHostTests/testTheAutomationCanSeeTheDevice
