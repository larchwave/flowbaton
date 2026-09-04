# Support Matrix

FlowBaton `v0.2.0-beta.5` is published for the Android and iOS Simulator YAML
CLI. This table separates beta evidence from the future v1 target; the beta
does not declare v1 complete.

| Host | Android beta | iOS Simulator beta | iOS device | Future v1 |
| --- | --- | --- | --- | --- |
| macOS arm64 | available | Xcode 26.2 tested | experimental | not declared |
| macOS amd64 | available | Xcode 26.2 tested | experimental | not declared |
| Linux amd64 | connected execution passed | unavailable | experimental | not declared |
| Windows amd64 | installer and driver provisioning passed<sup>1</sup> | unavailable | experimental | not declared |

<sup>1</sup> Connected Android execution was not run directly on Windows. The
connected release gate passed on Linux.

The beta release installed the exact archive and provisioned the production
driver from an empty home on Linux, Windows, Intel Mac, and Apple silicon Mac.
Its iOS Simulator packages executed natively on both Mac architectures with
Xcode 26.2 (build 17C52). The physical-iOS driver is present and still
hardening.

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
