# Beta readiness

The beta candidate is not published. The target is the existing YAML CLI on
Android and iOS Simulator, under [Release Policy](release-policy.md).
Physical iOS, AI exploration, and the multi-node server retain their
experimental status.

## Completed engineering work

- Screenshot output is confined to its output directory even through nested
  symlinks. Existing files and collision numbering are preserved.
- Android recording finalization rejects missing, empty, and nonregular host
  artifacts instead of reporting success after an invalid pull.
- iOS smoke assertions reach XCTest. Only active automation command failures
  become command errors, so release smoke failures cannot silently pass.
- Prerelease tags select the beta cask and GitHub prerelease status without
  replacing the stable cask or becoming the latest stable release. Invalid
  versions fail before packaging starts.

## Validation

On 2026-09-04, commit `e1c23e9` passed the complete
[GitHub CI run](https://github.com/larchwave/flowbaton/actions/runs/33917307622),
including Linux, Windows, macOS, race checks, real Chrome, Android
instrumentation, and iOS Simulator execution.

Signed tag `v0.2.0-beta.2` points to that commit. Its
[release pipeline](https://github.com/larchwave/flowbaton/actions/runs/33917755178)
checks the exact tagged source again. The earlier `beta.1` pipeline stopped
on Chrome startup; no release was published from it. Chrome now reports
bounded startup diagnostics and early process exits, and its live tests pass
in ordinary CI and the `beta.2` release job. The earlier failure's cause was
not established.

Local checks passed: `go test -p 1 ./...`, `go vet ./...`, race tests for
`internal/android`, `internal/cli`, and `internal/version`, 30 Swift tests,
strict Swift formatting, Android unit/lint/APK builds, and release static
checks. A CLI flow launched Settings, opened General, and verified About
on the iPhone 16e Simulator using a fresh runner build.

Local regression checks cover artifact confinement, recording validation,
XCTest issue routing, and beta channel selection. The iOS runner was rebuilt
and its Simulator smoke passed. A temporary deliberate assertion failure
returned Xcode exit 65 and the matching failure in the result bundle; after
removing it, the clean smoke passed again.

Local checks do not establish native Intel Mac, Linux, or Windows installation,
notarization, public attestation, or anonymous release delivery.

## Publication requirements still open

1. Populate the `release-signing` and `release` environments with dedicated
   signing/notarization and tap credentials. Both environments are configured
   for `v*` tags with review by `larchwave`; both currently contain zero secrets.
   Repository administration works through the dedicated `larchwave` Git
   configuration. Local Developer ID identities do not configure CI.
2. Complete the `beta.2` release pipeline. Candidate CI and the signed tag
   are verified. Its initial Intel iOS attempt failed before test initialization
   with an Accessibility startup timeout; the failed jobs were retried on a
   fresh runner. Android, Apple-silicon iOS, Go/race, and Chrome gates passed.
   The retry and subsequent packaging gates still need final results.
3. Pass every native host installer and Homebrew smoke, including Intel and
   Apple silicon; retain signed archives, checksums, SBOM, and provenance.
4. Pass the workflow's anonymous probe against the published tag, assets,
   policy, and beta cask. Do not retain a release when its gates fail.

After successful publication, the beta installation command is
`brew install --cask larchwave/flowbaton/flowbaton-beta`. Remove any stable
FlowBaton installation first: both channels install the same executable.
This is a future candidate path; a beta is not currently available.

## Script trust boundary

Run flows and JavaScript only from sources you trust. Session isolation is
not a security sandbox: scripts can use the configured environment and send
HTTP requests, including multipart uploads of files under the script's
directory. Those files are not individually enumerated by flow preflight.
Do not place untrusted scripts beside credentials or other private files.
