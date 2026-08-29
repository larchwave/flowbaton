# Device Driver Specification

## 1. Shared surface

`contracts/v0/driver.json` defines the platform-neutral request surface. The Go
interface in `internal/device` must cover that contract in both directions.

Driver operations include application lifecycle, hierarchy retrieval, element
interaction, text input, gestures, screenshots, recording, permissions,
location, media, links, clipboard access, and platform settings.

An operation that a platform cannot implement returns the shared unsupported
sentinel. Transport failures, timeouts, and invalid requests remain distinct so
the engine can make a safe retry decision.

## 2. Android

The Android host uses ADB for package lifecycle, port forwarding, device files,
settings, media, and agent startup. On-screen operations use the loopback agent
transport.

Managed startup performs these steps:

1. locate or build the application and instrumentation packages;
2. install both packages when required;
3. create an ADB port forward;
4. start instrumentation;
5. wait for transport readiness;
6. remove owned forwards and processes during cleanup.

Hierarchy bounds and gesture coordinates use device pixels. WebView nodes may be
merged only when the flow enables the Android developer-tools mode.

`addMedia` requires Android API 29 or newer because the agent uses the scoped
MediaStore insertion path. When a prepared program contains that command, the
host reads `ro.build.version.sdk` before managed startup and refuses API 26--28
before uninstalling or installing driver packages.

## 3. iOS Simulator

The iOS host divides work between `simctl` and the XCTest runner. `simctl` owns
simulator boot state, application installation, launch, termination, privacy,
location, media, screenshots, and keychain reset. The runner owns hierarchy,
gestures, typing, clipboard access, and other in-session UI operations.

Managed startup builds or locates the XCTest bundle, starts the runner on a
host-selected port with a fresh launch id (`FLOWBATON_RUNNER_ID`), waits for a
`/status` answer that echoes that id in `runner`, and terminates only the
process it started. A port that answers before the start, or answers with
another id or none, belongs to someone else and is refused. Operator-started
mode remains available when the host has no managed bundle; it checks health
without an id.

Hierarchy bounds and gesture coordinates use points. Screenshot crops convert
point-space bounds to the captured pixel dimensions.

Orientation values use the four canonical flow enums: `PORTRAIT`,
`LANDSCAPE_LEFT`, `LANDSCAPE_RIGHT`, and `UPSIDE_DOWN`. The host maps them to
the XCTest runner's lower-camel-case wire values without dropping underscores
before lookup.

## 4. Web

The web driver owns browser lifecycle, page navigation, hierarchy extraction,
screen capture, and browser input. Web runs use the flow URL as their effective
application target. Unsupported mobile-only operations return the shared
unsupported sentinel.

## 5. Resource ownership

Every session records which processes, ports, forwards, temporary files, and
recordings it created. Cleanup removes owned resources and leaves operator-owned
resources unchanged.
