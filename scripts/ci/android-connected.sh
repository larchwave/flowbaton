#!/usr/bin/env bash
set -euo pipefail

api="${ANDROID_API_LEVEL:-34}"
image="system-images;android-${api};google_apis;x86_64"
avd="flowbaton-ci-api-${api}"

sdkmanager 'platform-tools' 'emulator' "$image"
printf 'no\n' | avdmanager create avd --force --name "$avd" --package "$image" --device 'pixel_6'

emulator -avd "$avd" -no-window -no-audio -no-boot-anim -gpu swiftshader_indirect -no-snapshot -wipe-data >"${RUNNER_TEMP:-/tmp}/flowbaton-emulator.log" 2>&1 &
emulator_pid=$!
trap 'kill "$emulator_pid" 2>/dev/null || true' EXIT INT TERM

adb wait-for-device
deadline=$((SECONDS + 300))
until [[ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" == "1" ]]; do
  if (( SECONDS >= deadline )); then
    tail -200 "${RUNNER_TEMP:-/tmp}/flowbaton-emulator.log" >&2 || true
    exit 1
  fi
  sleep 2
done
adb shell input keyevent 82 || true

./gradlew --no-daemon --dependency-verification strict connectedDebugAndroidTest
