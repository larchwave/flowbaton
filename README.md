# FlowBaton

**Mobile UI tests that fail only when your app breaks.**

One Go binary drives Android devices, iOS Simulators, and physical iPhones
from plain YAML flows — or explores your app with an AI crew and writes the
flows itself. Local runs need no server process, JVM, or device cloud.

[![CI](https://github.com/larchwave/flowbaton/actions/workflows/ci.yml/badge.svg)](https://github.com/larchwave/flowbaton/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/larchwave/flowbaton)](https://github.com/larchwave/flowbaton/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/larchwave/flowbaton)](https://goreportcard.com/report/github.com/larchwave/flowbaton)
[![License](https://img.shields.io/github/license/larchwave/flowbaton)](LICENSE)

<p align="center">
  <img src="docs/assets/demo.gif" width="280" alt="A FlowBaton flow driving the Android Settings app on an emulator">
</p>
<p align="center"><sub>Captured with <code>flowbaton record</code> — the same
command you get out of the box.</sub></p>

```yaml
appId: com.example.app
---
- launchApp
- tapOn: "Log in"
- inputText: "demo@example.com"
- tapOn: "Continue"
- assertVisible: "Welcome back"
```

## Install

```sh
brew tap larchwave/flowbaton
brew trust larchwave/flowbaton
brew install flowbaton
```

Or with Go (a source build reports its version as `dev`):

```sh
go install github.com/larchwave/flowbaton/cmd/flowbaton@latest
```

On Windows, download `flowbaton_<version>_windows_amd64.zip` from the
releases page, extract it, and put the extracted folder on `PATH`. Windows is
a pre-alpha target: flow parsing and syntax checks work, Android execution
has not yet passed the release gates, and iOS work needs macOS — see the
[support matrix](docs/support-matrix.md).

Archives for macOS, Linux, and Windows are on the
[releases page](https://github.com/larchwave/flowbaton/releases). Every archive
carries SLSA provenance:

```sh
gh attestation verify flowbaton_*_darwin_arm64.tar.gz --repo larchwave/flowbaton
```

## Quick start

Check a flow without starting a device:

```sh
printf 'appId: com.example.app\n---\n- tapOn: "Continue"\n' | flowbaton check-syntax -
```

Prepare a platform driver and run a flow:

```sh
flowbaton driver-setup -p android
flowbaton test -p android --device emulator-5554 path/to/flow.yaml
```

The Android run needs a configured Android SDK and a running emulator or
connected device.

For iOS Simulators (needs Xcode and an installed Simulator runtime):

```sh
flowbaton driver-setup -p ios
flowbaton test -p ios --device <simulator-udid> path/to/flow.yaml
```

Find a booted Simulator's UDID with `xcrun simctl list devices booted`.

For a physical iPhone or iPad (iOS 17+), build the device runner once —
this needs Xcode with a signed-in Apple ID (a free account works, with
7-day profiles) — then run flows over USB; no sudo, no extra daemons:

```sh
export FLOWBATON_IOS_TEAM=<your team id>   # Xcode > Settings > Accounts
scripts/build-ios-device-runner.sh
flowbaton test -p ios --device <device-udid> path/to/flow.yaml
```

`flowbaton list-devices -p ios` shows attached hardware alongside
Simulators; the same `-p ios` picks the right driver from the UDID.
Record a run to a video with `flowbaton record path/to/flow.yaml`. Run
`flowbaton` with no arguments to print the command summary.

Ready-made flows that run against the stock Android Settings app (no app to
build) live in
[flowbaton-samples](https://github.com/larchwave/flowbaton-samples).

## Let it explore

Give FlowBaton a provider key and it tests your app on its own:

```sh
export OPENAI_API_KEY=...   # or ANTHROPIC_API_KEY with FLOWBATON_AI_PROVIDER=anthropic
flowbaton explore --app com.example.app -p ios --device <simulator-udid>
```

A crew of models maps each screen, plans test scenarios, taps through them
on the live device, and judges the outcomes. The session ends with a
written report, and every passing run is exported as flow YAML through the
same parser that runs handwritten flows — a test the crew finds is a test
you commit.

- The crew acts only through a fixed device toolbox; every step and its
  effect on the screen is recorded.
- Per-app memory carries screen maps, plans, and operator hints into the
  next session.
- `--pilot` adds a supervisor model that reviews progress and stops a
  runaway session.
- `--max-tests` and `--max-steps` bound every session; `--record` captures
  video.
- Without a key, explore refuses to start. Your key, your models, no
  telemetry.

## Use it from a coding agent

FlowBaton ships an MCP server inside the same binary:

```sh
flowbaton mcp
```

Point your agent's MCP configuration at that command and it gets
`check_syntax`, `list_devices`, `start_device`, `hierarchy`, `query`,
`run_flow`, `screenshot`, and `explore` tools against connected devices and
Simulators — enough for an agent to boot a device, drive a flow, read the
screen, and kick off an autonomous session. `AGENTS.md` in this repository
gives agents a short tour of the CLI and the flow shape.

## Why FlowBaton

- **One static binary.** The Go host and the on-device drivers ship together.
  Nothing else to install, no background server to babysit.
- **Deterministic on purpose.** The host and the drivers speak frozen,
  machine-readable contracts (`contracts/`), and selector semantics are pinned
  by tests. An upgrade will not quietly change what `tapOn` matches.
- **Preflight before mutation.** An unsupported capability fails the run
  before FlowBaton touches your device. No half-configured emulators.
- **AI is optional and yours.** Bring your own key for AI-assisted commands
  and exploration. Without a key those commands fail closed instead of
  silently degrading. No telemetry either way.
- **It writes flows, not just runs them.** Exploration exports its passing
  runs through the same parser that executes them, so generated tests are
  valid by construction.
- **From laptop to fleet.** `flowbaton serve` runs the same flows as a
  multi-node runtime with PostgreSQL holding every lease, token, and work
  claim — device sessions survive reconnects and cancel cleanly. See
  [Remote DeviceSession runtime](docs/remote-runtime.md).
- **Auditable releases.** Archives are built by a pinned least-privilege CI
  workflow and ship SLSA provenance you can verify yourself.

## Status

FlowBaton is pre-alpha. Android devices and emulators plus iOS Simulators
work today; physical iOS devices, autonomous exploration, and the
multi-node runtime are newly landed and hardening. The released archives
predate the newest features: until the next tag, `explore`, the
`start_device`/`run_flow`/`screenshot` MCP tools, and the physical-iOS
driver need a source build (`go install` above). Command surface and
contracts are versioned; expect breaking changes between pre-1.0 releases.

## Project documents

- [Development plan](PLAN.md)
- [Support matrix](docs/support-matrix.md)
- [Release policy](docs/release-policy.md)
- [Dependency policy](docs/dependency-policy.md)
- [Remote DeviceSession runtime](docs/remote-runtime.md)
- [Security policy](SECURITY.md)

The machine-readable API contracts live in `contracts/`. Product behavior is
specified in `specs/`.

## Community

- Questions and ideas: [GitHub Discussions](https://github.com/larchwave/flowbaton/discussions)
- Bugs: [issues](https://github.com/larchwave/flowbaton/issues)
- Want to help? Read [CONTRIBUTING.md](CONTRIBUTING.md) — it also holds the
  build-from-source and platform check commands.

If FlowBaton is useful to you, star the repository — it helps other people
find it.

## License

FlowBaton is licensed under the [Apache License 2.0](LICENSE). Third-party
components and their licenses are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
