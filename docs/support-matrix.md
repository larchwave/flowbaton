# Support Matrix

FlowBaton is pre-alpha. The rows below are release targets, not a claim of a
published support window. Each target remains `ga-gated` until its public v1
release checks pass.

| Host | Android | iOS Simulator | Status |
| --- | --- | --- | --- |
| macOS arm64 | planned v1 | planned v1 | ga-gated |
| macOS amd64 | planned v1 | planned v1 | ga-gated |
| Linux amd64 | planned v1 | unavailable | ga-gated |
| Windows amd64 | planned v1 | unavailable | ga-gated |

The local tree also contains web execution, MCP tools, and provider-backed AI
commands. These surfaces remain pre-release and require their own configured
runtime dependencies.

Unsupported platform operations must fail before device mutation. Missing SDKs,
driver artifacts, devices, or provider credentials must produce a clear error.

Physical iOS devices, hosted accounts, billing, telemetry, and remote render
services are outside the v1 support target.
