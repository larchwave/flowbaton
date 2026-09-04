# Beta readiness

`v0.2.0-beta.5` is the published beta for the YAML CLI on Android and iOS
Simulator. It is a GitHub prerelease and uses the separate
`flowbaton-beta` Homebrew cask. Physical iOS, AI exploration, and the
multi-node server retain their experimental status. This beta does not
declare the future v1 target complete.

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
- Mobile driver downloads stay outside the Git checkout while GoReleaser
  validates a clean source tree. Full payload size and digest checks remain.
- Chrome startup failures report bounded output and early process exits.
- The Windows smoke uses an isolated writable home, macOS smokes resolve the
  physical temporary directory, and driver extraction validates complete tar
  streams including trailing padding.
- Release builds and installer smokes select Xcode 26.2 (build 17C52), matching
  the packaged iOS Simulator runner compatibility contract.

## Validation

Signed tag `v0.2.0-beta.5` points to commit `bcff97b`. Its complete
[release pipeline](https://github.com/larchwave/flowbaton/actions/runs/33926562424)
finished successfully on 2026-09-04 and published the
[GitHub prerelease](https://github.com/larchwave/flowbaton/releases/tag/v0.2.0-beta.5).

The exact tagged source passed Go tests with the race detector, contracts and
release-policy checks, real Chrome execution, connected Android execution on
Linux, and native iOS Simulator execution on Intel and Apple silicon. The iOS
jobs used Xcode 26.2 (build 17C52).

The shipped Darwin archives passed Developer ID signing, notarization, and
ticket verification. The candidate also passed checksum and SBOM generation,
provenance attestation, clean-host archive installation and production driver
provisioning on Linux amd64, Windows amd64, macOS amd64, and macOS arm64, plus
Homebrew installation on both Mac architectures. The final anonymous probe
retrieved the published tag, release assets, installers, attestation, beta
cask, and project policy without release credentials.

The Windows gate installed the archive and completed Android driver
provisioning. Connected Android execution was not run directly on Windows;
that device gate passed on Linux.

## Install the beta

```sh
brew tap larchwave/flowbaton
brew trust larchwave/flowbaton
brew install --cask flowbaton-beta
```

The stable cask remains at `v0.1.1` and installs as `flowbaton`. Remove one
channel before installing the other because both provide the `flowbaton`
executable.

## Script trust boundary

Run flows and JavaScript only from sources you trust. Session isolation is
not a security sandbox: scripts can use the configured environment and send
HTTP requests, including multipart uploads of files under the script's
directory. Those files are not individually enumerated by flow preflight.
Do not place untrusted scripts beside credentials or other private files.
