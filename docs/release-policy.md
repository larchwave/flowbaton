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
- native Developer ID signatures and accepted Apple notarization for every
  Darwin CLI archive, with Gatekeeper assessment on Intel and Apple silicon;
- an SBOM and build attestation bound to the shipped digests;
- installation smoke checks on every advertised host, including a real
  `brew install --cask` from the exact signed candidate on both Mac
  architectures before the tap advances;
- at least one Android and one iOS Simulator execution smoke;
- anonymous retrieval of every required surface in
  `governance/public-delivery-surfaces.json`;
- least-privilege publication credentials and immutable CI action pins.

Snapshots are local engineering artifacts and do not satisfy this gate.

## Beta channel

Beta candidates use a prerelease tag such as `v0.2.0-beta.1`. Release tags
must contain a strict semantic version without build metadata. Prereleases
pass the same signing, platform, installation, attestation, and anonymous
retrieval gates as stable releases. They are marked as GitHub prereleases
and never become the latest stable release.

Prereleases update `Casks/flowbaton-beta.rb`; stable releases update
`Casks/flowbaton.rb`. Installer smoke and anonymous probes check the selected
channel. Both casks provide the `flowbaton` executable, so install only one
channel at a time. A beta does not establish `distributed-v1` or expand the
support matrix. See [Beta readiness](beta-readiness.md) for current evidence.

## Enforcement

`.github/workflows/release-publish.yml` is the only publishing workflow. It
refuses lightweight or GitHub-unverified tags and binds every gate, candidate
archive, SBOM, checksum, installer, driver manifest, and provenance statement
to the tag's exact commit. The release stays draft until clean-host installer
smokes pass on Linux amd64, Windows amd64, macOS amd64, and macOS arm64 and the
architecture-specific iOS Simulator packages have each executed on their
matching native release host. Darwin signing runs only in the protected
`release-signing` environment and fails closed unless the Developer ID
certificate and exactly one complete notarization credential mode are present:
either an App Store Connect API key, or an Apple ID, team ID, and app-specific
password. Partial or mixed modes are rejected. Those credentials must be
dedicated to release signing and need only the access required by `notarytool`.
The tap deploy key must be a write-enabled key dedicated only to
`larchwave/homebrew-flowbaton`.

The candidate cask must install successfully through Homebrew on Intel and
Apple silicon before the tap advances. After the draft becomes public, a fresh
empty-home process with no GitHub, tap, or Apple credentials probes every
required entry in `governance/public-delivery-surfaces.json`. It checks the
public repository and exact tag, downloads every asset and installer, verifies
the checksum set, verifies the attestation bundle against the repository,
workflow, and tag, checks the published cask, and reads the release policy from
the exact commit. Any failure deletes the release and reverts the tap commit;
rollback failure is itself a hard failure requiring owner intervention.

Installers require the GitHub CLI and verify the downloaded archive with
`gh attestation verify`, constrained to `larchwave/flowbaton`, the release tag,
GitHub-hosted runners, and `.github/workflows/release-publish.yml`. A checksum
match by itself is not release identity evidence.

The driver assets are separate attested release archives. `android-agent`
contains `agent.apk` and `agent-androidTest.apk`. `ios-simulator-runner`
contains one relocatable `.xctestrun` plus every required product. The
attested `driver-manifest.json` is generated from the shipped bytes and is the
only release-eligible manifest; the committed fixture manifest remains
representative and cannot resolve in production.
