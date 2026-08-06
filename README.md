# FlowBaton

FlowBaton is a pre-alpha mobile UI automation toolkit. It provides a Go CLI,
an Android device agent, and an iOS Simulator runner under the Apache-2.0
license.

The repository currently supports local development and device testing. A
public v1 release has not been published.

## Requirements

- Go 1.25 or newer
- Android SDK and Java 17 for Android work
- Xcode with an installed iOS Simulator runtime for iOS work
- XcodeGen 2.44.1 for the iOS Xcode project

## Build and test

```sh
go test ./...
go vet ./...
go build -o ./flowbaton ./cmd/flowbaton
./flowbaton --version
```

Android checks:

```sh
cd drivers/android
./gradlew --no-daemon --dependency-verification strict \
  :core:test :agent:lintDebug :agent:assembleDebug :agent:assembleDebugAndroidTest
```

iOS checks:

```sh
xcodegen generate --spec drivers/ios/project.yml --project drivers/ios
swift test --package-path drivers/ios
xcrun swift-format lint --strict --recursive \
  drivers/ios/Sources drivers/ios/Tests drivers/ios/UITests
xcodebuild -project drivers/ios/FlowBatonIOSRunner.xcodeproj \
  -scheme FlowBatonIOSRunnerUITests \
  -destination 'generic/platform=iOS Simulator' \
  build-for-testing
```

## CLI

Inspect a flow without starting a device:

```sh
printf 'appId: com.example.app\n---\n- tapOn: "Continue"\n' | \
  go run ./cmd/flowbaton check-syntax -
```

Prepare a platform driver and run a flow:

```sh
go run ./cmd/flowbaton driver-setup -p android
go run ./cmd/flowbaton test -p android --device emulator-5554 path/to/flow.yaml
```

The device example requires a configured Android SDK, a running emulator, and a
flow file for the application under test.

Use `go run ./cmd/flowbaton` to print the current command summary.

## Project documents

- [Development plan](PLAN.md)
- [Support matrix](docs/support-matrix.md)
- [Release policy](docs/release-policy.md)
- [Dependency policy](docs/dependency-policy.md)
- [Security policy](SECURITY.md)

The machine-readable API contracts live in `contracts/`. Product behavior is
specified in `specs/`.
