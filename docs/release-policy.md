# Release Policy

FlowBaton is publicly distributed under Apache-2.0. The controlling product
decision is [DR-0001](decisions/0001-public-release.md).

## States

- `engineering-ready`: source, platform, packaging, and signing checks pass,
  but publication has not been proven.
- `externally-blocked`: local checks pass, but an owner-controlled account,
  signing identity, or public delivery surface is unavailable.
- `distributed-v1`: the signed tag, source, release assets, checksums, SBOM,
  install scripts, tap, and documentation are publicly retrievable and have
  passed the release smoke checks.

Only `distributed-v1` completes the v1 objective.

## Promotion gate

A public release requires:

- a clean signed tag;
- green Go, Android, iOS, contract, and policy checks;
- signed archives and a checksum file;
- an SBOM and build attestation bound to the shipped digests;
- installation smoke checks on every advertised host;
- at least one Android and one iOS Simulator execution smoke;
- anonymous retrieval of every required surface in
  `governance/public-delivery-surfaces.json`;
- least-privilege publication credentials and immutable CI action pins.

Snapshots are local engineering artifacts and do not satisfy this gate.

## Enforcement

`.github/workflows/release-publish.yml` is the only publishing workflow. It
refuses lightweight or GitHub-unverified tags and binds every gate, candidate
archive, SBOM, checksum, installer, driver manifest, and provenance statement
to the tag's exact commit. The release stays draft until clean-host installer
smokes pass on Linux amd64, Windows amd64, macOS amd64, and macOS arm64 and the
versioned Homebrew cask has advanced. A failure after the draft is created
deletes that draft; a failure while advancing the tap attempts to revert the
tap commit.

Installers require the GitHub CLI and verify the downloaded archive with
`gh attestation verify`, constrained to `larchwave/flowbaton`, the release tag,
GitHub-hosted runners, and `.github/workflows/release-publish.yml`. A checksum
match by itself is not release identity evidence.

The driver assets are separate attested release archives. `android-agent`
contains `agent.apk` and `agent-androidTest.apk`. `ios-simulator-runner`
contains one relocatable `.xctestrun` plus every referenced product. The
attested `driver-manifest.json` is generated from the shipped bytes and is the
only release-eligible manifest; the committed fixture manifest remains
representative and cannot resolve in production.
