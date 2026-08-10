# Support Matrix

FlowBaton is pre-alpha. The rows below are release targets, not a claim of a
published support window. Each target remains `ga-gated` until its public v1
release checks pass.

| Host | Android | iOS Simulator | iOS Device | Status |
| --- | --- | --- | --- | --- |
| macOS arm64 | planned v1 | planned v1 | planned v1 | ga-gated |
| macOS amd64 | planned v1 | planned v1 | planned v1 | ga-gated |
| Linux amd64 | planned v1 | unavailable | planned v1 | ga-gated |
| Windows amd64 | planned v1 | unavailable | planned v1 | ga-gated |

Physical iOS devices run over usbmuxd through the embedded go-ios transport.
Building the device runner needs Xcode and an Apple signing team (macOS only);
running an already-built runner works from any host with usbmuxd (macOS ships
it; Linux and Windows need it installed). iOS 17+ devices use an in-process
userspace tunnel — no sudo, no daemons. Continuous integration never touches
hardware: the device path is covered by seam-level tests, and the release-gate
evidence for hardware is the env-gated end-to-end run keyed by
`FLOWBATON_TEST_IOS_DEVICE_UDID`.

Two operations stay unsupported on physical iOS hardware because Apple locks
them for every automation tool without a jailbreak: keychain reset
(`clearKeychain`) and media injection (`addMedia`). They fail before device
mutation and report `false` in the driver capability document. Everything
else in the flow command surface works on physical devices.

Hardware behavior notes: `clearState` reinstalls the app from the archive named
by `FLOWBATON_IOS_APP_IPA` (hardware exposes no app container to preserve);
screen recordings are assembled from the instruments screenshot stream into
an MJPEG AVI container regardless of the requested file extension; device
logs stream from the syslog relay, capped by `FLOWBATON_IOS_DEVICE_LOG_LIMIT`
bytes per capture. `setPermissions` cannot pre-grant the way `simctl privacy`
does: the runner auto-answers the system permission dialogs as they appear
(`allow`/`deny`; `unset` does not exist on hardware), and `openLink` opens
through Safari.

The local tree also contains web execution, MCP tools, and provider-backed AI
commands. These surfaces remain pre-release and require their own configured
runtime dependencies.

Unsupported platform operations must fail before device mutation. Missing SDKs,
driver artifacts, devices, or provider credentials must produce a clear error.

Archive installation requires GitHub CLI attestation verification. Android
driver provisioning also requires the Android SDK identity tools; iOS Simulator
driver provisioning requires Xcode and `codesign`. These prerequisites are
exercised from an empty home directory by the tag release gate.

Hosted accounts, billing, telemetry, and remote render services are outside
the v1 support target.
