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
