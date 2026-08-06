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

## Local checks

Run the checks that cover your change. Go changes normally require:

```sh
go test ./...
go vet ./...
gofmt -l .
git diff --check
```

Android changes also require the Gradle checks in `README.md`. iOS changes also
require XcodeGen 2.44.1, the Swift package tests, strict formatting of Sources,
Tests, and UITests, and the generic Simulator build-for-testing command in
`README.md`.

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
