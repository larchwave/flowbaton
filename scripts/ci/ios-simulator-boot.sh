#!/usr/bin/env bash
# Picks one available iPhone Simulator, boots it, and prints its UDID.
#
# Shared by ios-simulator-test.sh and ios-settle-fixture-flow.sh so both act
# on the same device. FLOWBATON_CI_SIMULATOR_UDID pins the choice for a local
# run against a simulator that is already booted.
set -euo pipefail

if [ -n "${FLOWBATON_CI_SIMULATOR_UDID:-}" ]; then
  udid="$FLOWBATON_CI_SIMULATOR_UDID"
else
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
fi

xcrun simctl boot "$udid" 2>/dev/null || true
xcrun simctl bootstatus "$udid" -b >&2
printf '%s\n' "$udid"
