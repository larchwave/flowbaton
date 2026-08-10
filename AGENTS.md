# FlowBaton agent guide

FlowBaton is a mobile UI automation toolkit: a Go host CLI, an Android device
agent, and an iOS Simulator runner. Test flows are plain YAML.

## CLI surface

```sh
flowbaton check-syntax <file|->        # validate a flow without a device
flowbaton driver-setup -p <platform>   # prepare the android or ios driver
flowbaton test -p <platform> --device <id> <flow.yaml>
flowbaton                              # print the command summary
```

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
