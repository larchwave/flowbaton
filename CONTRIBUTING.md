# Contributing to FlowBaton

Issues and pull requests for the CLI, engine, drivers, tests, and docs.

## Before opening a change

- Keep the change to one behavior or one maintenance goal.
- Add or update tests for behavior changes.
- When a public shape changes, update the contracts and the specs in the
  same change.
- Do not commit credentials, device data, build output, or local logs.
- Add no runtime dependency without the license and notice updates in
  `docs/dependency-policy.md`.

## Build requirements

- Go 1.26 or newer (module floor 1.26; CI uses 1.26.1)
- Android SDK and Java 17 for Android work
- Xcode with an installed iOS Simulator runtime for iOS work
- XcodeGen 2.44.1 for the iOS Xcode project

## Local checks

Run the checks that cover your change. Go changes normally need:

```sh
go test ./...
go vet ./...
gofmt -l .
git diff --check
```

Android changes also need:

```sh
cd drivers/android
./gradlew --no-daemon --dependency-verification strict \
  :core:test :agent:lintDebug :agent:assembleDebug :agent:assembleDebugAndroidTest
```

iOS changes also need:

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

Imperative subject, then these trailers in the body:

```text
Confidence: high | medium | low
Scope-risk: concise risk statement
Tested: commands that ran
Not-tested: checks that did not run, with the reason
```

## Review

Describe the user-visible outcome, the boundaries that changed, the tests
that ran, and known risks. Maintainers may ask for a smaller change when
independent concerns are mixed together.
