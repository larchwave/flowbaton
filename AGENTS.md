# FlowBaton agent guide

FlowBaton is a mobile UI automation toolkit: a Go host CLI, an Android device
agent, and an iOS runner that drives Simulators and physical devices (iOS 17+
hardware over usbmuxd). Test flows are plain YAML. `-p ios` covers both iOS
flavors — the UDID decides whether a Simulator or an attached device runs.

## CLI surface

```sh
flowbaton check-syntax <file|->        # validate a flow without a device
flowbaton driver-setup -p <platform>   # prepare the android or ios driver
flowbaton test -p <platform> --device <id> <flow.yaml>
flowbaton record <flow.yaml> [out]     # run a flow and capture a video
flowbaton list-devices [-p platform]   # connected devices and simulators
flowbaton start-device -p <platform> --device <id>   # boot a simulator or launch an AVD
flowbaton hierarchy -p <platform>      # dump the current UI tree
flowbaton query -p <platform> <expr>   # find on-device elements
flowbaton bugreport -p <platform>      # capture device diagnostics
flowbaton explore --app <id> -p <platform>   # autonomous AI exploration of one app
flowbaton mcp                          # serve these as MCP tools over stdio
flowbaton serve                        # multi-node DeviceSession runtime (PostgreSQL)
flowbaton db apply-schema --database-url <url>
flowbaton auth keygen|cert-map        # serve-runtime credentials
flowbaton                              # print the command summary
```

Prefer `flowbaton mcp` when you can register MCP servers: it exposes
`check_syntax`, `list_devices`, `start_device`, `hierarchy`, `query`,
`run_flow`, `screenshot`, and `explore` without shelling out. `run_flow`
executes a flow on a device for real — pass `platform`, `udid`, and either
`path` (confined to the base directory) or inline `yaml`. `start_device`
boots a simulator or launches an emulator and waits for readiness. `explore`
runs an autonomous AI exploration session against one app (`appId` +
`platform`) and needs a configured AI provider on the host.

## Flow shape

```yaml
appId: com.example.app
---
- launchApp
- tapOn: "Continue"
- assertVisible: "Welcome"
```

Validate any flow you generate with `check-syntax` before offering it.

## Repository map

- `cmd/flowbaton` — process entry and command dispatch
- `internal/cli` — command parsing and host orchestration
- `internal/flow` — YAML decoding and source diagnostics
- `internal/engine` — deterministic command execution
- `internal/device` — platform-neutral driver surface
- `internal/capability` — side-effect-free preflight checks
- `drivers/android`, `drivers/ios` — on-device agent and runner code
- `contracts/` — machine-readable public and driver contracts
- `specs/` — normative product behavior

Contract and spec changes must land together with both producer and consumer
sides in one reviewed change.

## Quality gates

```sh
go test ./...
go vet ./...
gofmt -l .
git diff --check
```

Platform-specific checks live in [CONTRIBUTING.md](CONTRIBUTING.md).
