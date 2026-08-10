# Contributing to FlowBaton

FlowBaton accepts focused issues and pull requests for the CLI, engine, device
drivers, tests, and documentation.

## Before opening a change

- Keep the change limited to one behavior or maintenance goal.
- Add or update tests for behavior changes.
- Update contracts and specifications when a public shape changes.
- Do not commit credentials, device data, build output, or local logs.
- Add no runtime dependency without the license and notice updates required by
  `docs/dependency-policy.md`.

## Build requirements

- Go 1.25 or newer
- Android SDK and Java 17 for Android work
- Xcode with an installed iOS Simulator runtime for iOS work
- XcodeGen 2.44.1 for the iOS Xcode project

## Local checks

Run the checks that cover your change. Go changes normally require:

```sh
go test ./...
go vet ./...
gofmt -l .
git diff --check
```

Android changes also require:

```sh
cd drivers/android
./gradlew --no-daemon --dependency-verification strict \
  :core:test :agent:lintDebug :agent:assembleDebug :agent:assembleDebugAndroidTest
```

iOS changes also require:

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

## Commit messages

Use an imperative subject and include these trailers in the commit body:

```text
Confidence: high | medium | low
Scope-risk: concise risk statement
Tested: commands that ran
Not-tested: checks that did not run, with the reason
```

## Review

Describe the user-visible outcome, changed boundaries, test results, and known
risks. Maintainers may ask for a smaller change when independent concerns are
mixed together.
