# FlowBaton

[![CI](https://github.com/larchwave/flowbaton/actions/workflows/ci.yml/badge.svg)](https://github.com/larchwave/flowbaton/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/larchwave/flowbaton)](https://github.com/larchwave/flowbaton/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/larchwave/flowbaton)](https://goreportcard.com/report/github.com/larchwave/flowbaton)
[![License](https://img.shields.io/github/license/larchwave/flowbaton)](LICENSE)

Mobile UI automation for Android and iOS. One Go binary: CLI, MCP, and
optional explore with your own key. No telemetry.

<p align="center">
  <img src="docs/assets/hero.webp" width="720" alt="A hand holding a written flow passes a baton to a robotic arm reaching out of a phone screen, which taps another device">
</p>

The usual way to drive a device is a YAML flow:

```yaml
appId: com.example.app
---
- launchApp
- tapOn: "Log in"
- inputText: "demo@example.com"
- tapOn: "Continue"
- assertVisible: "Welcome back"
```

```sh
flowbaton test -p ios --device <simulator-udid> login.yaml
```

The same file is valid on Android. Unsupported commands fail in preflight,
before the device is touched.

## Install

```sh
brew tap larchwave/flowbaton
brew trust larchwave/flowbaton
brew install flowbaton
```

`brew trust` is required for third-party casks on current Homebrew.

<details>
<summary>Go, archives, and Windows</summary>

From source. A source build reports its version as `dev`:

```sh
go install github.com/larchwave/flowbaton/cmd/flowbaton@latest
```

Archives for macOS, Linux, and Windows are on the
[releases](https://github.com/larchwave/flowbaton/releases) page. Each archive
ships SLSA provenance:

```sh
gh attestation verify flowbaton_*_darwin_arm64.tar.gz --repo larchwave/flowbaton
```

On Windows, download `flowbaton_<version>_windows_amd64.zip`, extract it, and
put that folder on `PATH`.

</details>

## Quick start

Parse a flow with no device attached:

```sh
printf 'appId: com.example.app\n---\n- tapOn: "Continue"\n' | flowbaton check-syntax -
```

Prepare a driver once, then run flows:

```sh
flowbaton driver-setup -p android
flowbaton test -p android --device emulator-5554 path/to/flow.yaml
```

<details>
<summary>iOS Simulator</summary>

Needs Xcode and an installed Simulator runtime.

```sh
flowbaton driver-setup -p ios
flowbaton test -p ios --device <simulator-udid> path/to/flow.yaml
```

A booted Simulator's UDID: `xcrun simctl list devices booted`.

</details>

<details>
<summary>Physical iPhone or iPad (iOS 17+)</summary>

The hardware driver is in this tree and still hardening. Build the device
runner once (Xcode, signed-in Apple ID; a free account works, with 7-day
profiles), then run over USB. No sudo.

```sh
export FLOWBATON_IOS_TEAM=<your team id>   # Xcode > Settings > Accounts
scripts/build-ios-device-runner.sh
flowbaton test -p ios --device <device-udid> path/to/flow.yaml
```

`flowbaton list-devices -p ios` lists attached hardware next to Simulators.
`-p ios` picks the driver from the UDID.

`clearKeychain` and `addMedia` are unsupported on hardware (Apple). They fail
before mutation. Details in the [support matrix](docs/support-matrix.md).

</details>

Sample flows against stock Android Settings, nothing to build:
[flowbaton-samples](https://github.com/larchwave/flowbaton-samples).

<p align="center">
  <img src="docs/assets/demo.gif" width="260" alt="A FlowBaton flow driving the Android Settings app on an emulator">
  <br>
  <sub>One of those samples, captured with <code>flowbaton record</code>.</sub>
</p>

## Explore

With a provider key, `explore` drives the app:

```sh
export OPENAI_API_KEY=...   # or ANTHROPIC_API_KEY with FLOWBATON_AI_PROVIDER=anthropic
flowbaton explore --app com.example.app -p ios --device <simulator-udid>
```

It maps screens, plans scenarios, taps through them on the device, writes a
report, and exports every passing run as flow YAML. Without a key, explore
refuses to start.

`--max-tests` and `--max-steps` cap a session. `--pilot` adds a supervisor
model. `--record` adds video. Per-app memory keeps screen maps, plans, and
operator hints for the next session.

## MCP

```sh
flowbaton mcp
```

Point an agent's MCP config at that command. Tools: `check_syntax`,
`list_devices`, `start_device`, `hierarchy`, `query`, `run_flow`,
`screenshot`, `explore`. [AGENTS.md](AGENTS.md) is the short tour for agents.

## Serve

`flowbaton serve` runs the same flows as a multi-node runtime. PostgreSQL holds
leases, tokens, and work claims. Device sessions survive reconnects and cancel
cleanly.

```sh
flowbaton db apply-schema --database-url "$FLOWBATON_DATABASE_URL"
```

A node then serves Integration v1 and DeviceSession v1 over mutual TLS.
Flags, certificates, and inventory:
[Remote DeviceSession runtime](docs/remote-runtime.md).

## Flow commands

| Group | Commands |
| --- | --- |
| App | `launchApp` `stopApp` `killApp` `clearState` `clearKeychain` `setPermissions` |
| Touch | `tapOn` `doubleTapOn` `longPressOn` `swipe` `scroll` `scrollUntilVisible` `back` `pressKey` `waitForAnimationToEnd` |
| Text | `inputText` `eraseText` `pasteText` `hideKeyboard` `copyTextFrom` `setClipboard` |
| Random text | `inputRandomText` `inputRandomNumber` `inputRandomEmail` `inputRandomPersonName` `inputRandomCityName` `inputRandomCountryName` `inputRandomColorName` |
| Assertions | `assertVisible` `assertNotVisible` `assertTrue` `assertScreenshot` |
| AI assertions | `assertWithAI` `assertNoDefectsWithAI` `extractTextWithAI` |
| Device | `setLocation` `travel` `setOrientation` `setAirplaneMode` `toggleAirplaneMode` `openLink` `openBrowser` `addMedia` |
| Capture | `takeScreenshot` `startRecording` `stopRecording` |
| Control | `runFlow` `repeat` `retry` `extendedWaitUntil` `action` |
| Scripting | `runScript` `evalScript` |

Text typed into a secure field is exported as a `${FLOWBATON_…SECRET…}`
placeholder. The engine fails the flow when that variable is unset, and keeps
the placeholder, never the resolved value, in recordings, artifacts, and
replays.

## CLI

| Command | What it does |
| --- | --- |
| `check-syntax` | Parse a flow and print source diagnostics. No device. |
| `test` | Run flows on a device, emulator, or Simulator. |
| `record` | Run one flow and render a video of it. |
| `explore` | Autonomous AI session; exports passing runs as YAML. |
| `list-devices` | Inventory devices and Simulators. Reads only. |
| `start-device` | Boot an emulator or Simulator, optionally creating it. |
| `hierarchy` | Dump what is on screen. Taps nothing. |
| `query` | Match a selector against the live screen. |
| `bugreport` | Collect a device diagnostic bundle. |
| `driver-setup` | Install the signed, version-matched platform driver. |
| `mcp` | Serve the MCP tool surface over stdio. |
| `serve` | Multi-node DeviceSession runtime (PostgreSQL). |
| `db` | Apply the `serve` runtime schema. |
| `auth` | Keys and certificate mapping for `serve`. |
| `generate-completion` | bash or zsh completion script. |

`flowbaton` with no arguments prints the command summary.

## Platforms

| Host | Android | iOS Simulator | iOS device (17+) |
| --- | --- | --- | --- |
| macOS arm64 / amd64 | works | works | in tree, hardening |
| Linux amd64 | works | unavailable | hardening<sup>1</sup> |
| Windows amd64 | pre-alpha<sup>2</sup> | unavailable | hardening<sup>1</sup> |

<sup>1</sup> Needs usbmuxd (macOS ships it). The device runner is built once on
macOS with Xcode; an already-built runner can run from any host.
<sup>2</sup> Flow parsing and syntax checks work. Android execution has not
passed the release gates.

Full detail: [support matrix](docs/support-matrix.md).

## Status

FlowBaton is pre-alpha. Android devices, emulators, and iOS Simulators work
today. Physical iOS, explore, and `serve` are in this tree and still
hardening.

The Homebrew bottle is v0.1.1. Until the next tag, `explore`, the
`start_device` / `run_flow` / `screenshot` MCP tools, and the physical-iOS
driver need a source build (`go install` above).

[Beta readiness](docs/beta-readiness.md) tracks candidate fixes, validation,
and the remaining publication gates. No beta has been published yet.

Command surface and contracts are versioned. Breaking changes are likely
before 1.0.

## Documentation

| | |
| --- | --- |
| [Roadmap](PLAN.md) | v1 target and what is still open |
| [Support matrix](docs/support-matrix.md) | What runs where |
| [Remote DeviceSession runtime](docs/remote-runtime.md) | `flowbaton serve` |
| [Release policy](docs/release-policy.md) | Versioning and release gates |
| [Dependency policy](docs/dependency-policy.md) | What may enter the build |
| [Security policy](SECURITY.md) | Reporting a vulnerability |
| [Contributing](CONTRIBUTING.md) | Build from source, platform checks |
| [Agent guide](AGENTS.md) | CLI and flow shape for coding agents |

Machine-readable contracts live in `contracts/`. Product behavior is specified
in `specs/`.

Questions: [Discussions](https://github.com/larchwave/flowbaton/discussions).
Bugs: [issues](https://github.com/larchwave/flowbaton/issues).

## License

[Apache License 2.0](LICENSE). Third-party components:
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
