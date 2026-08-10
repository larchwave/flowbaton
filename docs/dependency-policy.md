# Dependency Policy

FlowBaton keeps the execution core small and uses maintained libraries where a
platform or protocol would otherwise require a fragile custom implementation.

## Rules

Every new direct dependency must include:

- a concrete product need;
- the smallest supported version that satisfies that need;
- a license review;
- committed lock data or module checksums;
- focused tests at the integration boundary;
- an update to `THIRD_PARTY_NOTICES.md` when shipped code changes.

CI actions must use immutable commit pins. Toolchains and build plugins are
pinned to fixed versions. Automated updates must preserve these controls and
pass the full repository checks.

## Toolchain pins

| Tool | Version | Role |
| --- | --- | --- |
| Go | 1.26.1 in CI; module floor 1.26 (raised by go-ios v1.2.0) | Host, CLI, and contract tests |
| GoReleaser | 2.17.0 | Archives, checksums, and SBOM generation |
| Syft | 1.42.3 | Release SBOM generation |
| XcodeGen | 2.44.1 | Build-only MIT-licensed Xcode project generator downloaded by CI; the release archive SHA-256 is `a2e905fb68446e9bb4008cdfe2e13e3f176d0cbcca828b71770f8e53fca91b73`; it is not shipped in FlowBaton |
| Gradle | 8.5 | Android build wrapper |
| Android Gradle Plugin | 8.3.2 | Android application and test packages |
| Kotlin Gradle plugin | 1.9.22 | Kotlin compilation |
| Android SDK | API 34; build-tools 34.0.0 | Android CI build surface |
| AndroidX Test runner | 1.5.2 | Instrumentation runner |
| AndroidX Test JUnit | 1.1.5 | JUnit instrumentation adapter |
| gRPC Java | 1.81.0 | Android loopback transport |
| JUnit | 4.13.2 | Android JVM tests |

## Go modules

The direct Go modules are declared in `go.mod`; their resolved checksums are in
`go.sum`. The current direct modules provide:

- YAML node decoding and source locations;
- an embedded ECMAScript runtime;
- HTTP/2 support for Android transport;
- MCP server support;
- multimodal AI provider clients.
- PostgreSQL transactions, connection pooling, advisory-lock schema application, and
  `LISTEN/NOTIFY`-ready distributed session coordination through `pgx/v5`.
- physical iOS device transport (usbmuxd enumeration, app install, XCUITest
  launch, port forwarding, iOS 17+ tunneling) through `go-ios`.

`github.com/danielpaulus/go-ios` v1.2.0 is the minimum supported physical-iOS
transport. It is MIT licensed and is used instead of shelling out to a
separately installed go-ios binary so FlowBaton stays a single self-contained
binary with typed calls; its declared `go 1.26` raised the module floor from
1.25 to 1.26 (CI already runs 1.26.1). Every go-ios import is confined to
`internal/iosdevice`, whose boundary tests pin the exact symbols FlowBaton
calls; a go-ios API break fails at compile/test time, not on a device.
Hardware integration tests run only against the device named by
`FLOWBATON_TEST_IOS_DEVICE_UDID`; absence of that variable is the sole skip
path.

`github.com/jackc/pgx/v5` v5.10.0 is the minimum supported PostgreSQL driver.
It is MIT licensed, supports the module's Go floor, and is used instead of a
database-agnostic abstraction because lease fencing and schema changes require
PostgreSQL-specific transaction and advisory-lock behavior. Its integration
tests run only against the disposable database named by
`FLOWBATON_TEST_POSTGRES_URL`; absence of that variable is the sole skip path.

Run these checks after a module change:

```sh
go mod tidy
go mod verify
go test ./...
go vet ./...
go list -m all
```

Confirm the compiled package graph for each changed binary or runtime path with
`go list -deps`. Release packaging must reconcile the resolved graph with
`THIRD_PARTY_NOTICES.md` and the generated SBOM.

## Android dependencies

Android versions and checksums are locked under `drivers/android`. Builds must
use strict dependency verification:

```sh
cd drivers/android
./gradlew --no-daemon --dependency-verification strict \
  :core:test :agent:lintDebug :agent:assembleDebug :agent:assembleDebugAndroidTest
```

Do not distribute a host binary or device package until its resolved graph and
required notices are included in the release review.
