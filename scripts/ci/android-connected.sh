#!/usr/bin/env bash
set -euo pipefail

api="${ANDROID_API_LEVEL:-34}"
image="system-images;android-${api};google_apis;x86_64"
avd="flowbaton-ci-api-${api}"
port=5554
serial="emulator-${port}"
log="${RUNNER_TEMP:-/tmp}/flowbaton-emulator.log"
readiness_timeout="${ANDROID_EMULATOR_READY_TIMEOUT_SECONDS:-300}"
android_sdk="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
if [[ -z "$android_sdk" ]]; then
  printf 'ANDROID_SDK_ROOT or ANDROID_HOME must identify the Android SDK\n' >&2
  exit 1
fi
emulator_bin="${android_sdk}/emulator/emulator"

sdkmanager 'platform-tools' 'emulator' "$image"
printf 'no\n' | avdmanager create avd --force --name "$avd" --package "$image" --device 'pixel_6'

if [[ ! -x "$emulator_bin" ]]; then
  printf 'Android emulator executable not found at %s\n' "$emulator_bin" >&2
  exit 1
fi
"$emulator_bin" -version
"$emulator_bin" -avd "$avd" -port "$port" -no-window -no-audio -no-boot-anim -gpu swiftshader -no-snapshot -wipe-data >"$log" 2>&1 &
emulator_pid=$!
cleanup() {
  watchdog_pid=""
  if kill -0 "$emulator_pid" 2>/dev/null; then
    kill "$emulator_pid" 2>/dev/null || true
    (sleep 10; kill -KILL "$emulator_pid" 2>/dev/null || true) &
    watchdog_pid=$!
  fi
  wait "$emulator_pid" 2>/dev/null || true
  if [[ -n "$watchdog_pid" ]]; then
    kill "$watchdog_pid" 2>/dev/null || true
    wait "$watchdog_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

deadline=$((SECONDS + readiness_timeout))
while true; do
  if ! kill -0 "$emulator_pid" 2>/dev/null; then
    emulator_status=0
    wait "$emulator_pid" 2>/dev/null || emulator_status=$?
    printf 'Android emulator exited with status %d before becoming ready\n' "$emulator_status" >&2
    tail -200 "$log" >&2 || true
    exit 1
  fi
  boot_completed="$(timeout --kill-after=2s 5s adb -s "$serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r' || true)"
  if [[ "$boot_completed" == "1" ]] && timeout --kill-after=2s 5s adb -s "$serial" shell /system/bin/pm path android >/dev/null 2>&1; then
    break
  fi
  if (( SECONDS >= deadline )); then
    printf 'Android emulator did not become ready within %s seconds\n' "$readiness_timeout" >&2
    timeout --kill-after=2s 5s adb devices -l >&2 || true
    tail -200 "$log" >&2 || true
    exit 1
  fi
  sleep 2
done
timeout --kill-after=2s 5s adb -s "$serial" shell input keyevent 82 || true

ANDROID_SERIAL="$serial" ./gradlew --no-daemon --dependency-verification strict connectedDebugAndroidTest
