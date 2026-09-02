# Support Matrix

FlowBaton is pre-alpha. This table is the v1 release target, not a published
support window. A host stays closed until that target's v1 checks pass.

| Host | Android | iOS Simulator | iOS device | v1 |
| --- | --- | --- | --- | --- |
| macOS arm64 | v1 target | v1 target | after v1 | closed |
| macOS amd64 | v1 target | v1 target | after v1 | closed |
| Linux amd64 | v1 target | unavailable | after v1 | closed |
| Windows amd64 | v1 target | unavailable | after v1 | closed |

Today, in this tree and not as a support promise: Android devices and
emulators run, iOS Simulator runs, Windows Android execution has not passed
the release gates. The physical-iOS driver is present and still hardening.

Physical iOS (iOS 17+) talks over usbmuxd through the embedded go-ios
transport. Building the device runner needs Xcode and an Apple signing team,
macOS only. An already-built runner can run from any host with usbmuxd
(macOS ships it; Linux and Windows need it installed). iOS 17+ uses an
in-process userspace tunnel: no sudo, no extra daemons. CI never touches
hardware. Seam-level tests cover the device path; the release-gate evidence
is the env-gated end-to-end run keyed by `FLOWBATON_TEST_IOS_DEVICE_UDID`.

Two operations stay unsupported on physical iOS hardware because Apple locks
them without a jailbreak: keychain reset (`clearKeychain`) and media
injection (`addMedia`). They fail before device mutation and report `false`
in the driver capability document. The rest of the command surface is
implemented; hardware evidence is still required.

Hardware notes, once that evidence exists:

- `clearState` reinstalls the app from the archive named by
  `FLOWBATON_IOS_APP_IPA` (hardware exposes no app container to preserve).
- Screen recordings are assembled from the instruments screenshot stream
  into an MJPEG AVI container, regardless of the requested file extension.
- Device logs stream from the syslog relay, capped by
  `FLOWBATON_IOS_DEVICE_LOG_LIMIT` bytes per capture.
- `setPermissions` cannot pre-grant the way `simctl privacy` does. The
  runner auto-answers system permission dialogs as they appear
  (`allow` / `deny`; `unset` does not exist on hardware).
- `openLink` opens through Safari.

The tree also contains web execution, MCP tools, and provider-backed AI
commands. Those surfaces need their own configured runtime dependencies and
are not part of the v1 support target.

Unsupported platform operations must fail before device mutation. Missing
SDKs, driver artifacts, devices, or provider credentials must produce a
clear error.

Archive installation requires GitHub CLI attestation verification. Android
driver provisioning also requires the Android SDK identity tools. iOS
Simulator driver provisioning requires Xcode and `codesign`. The tag release
gate exercises these from an empty home directory.

Hosted accounts, billing, telemetry, and remote render services are outside
the v1 support target.
