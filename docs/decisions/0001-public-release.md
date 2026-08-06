# DR-0001: Public Apache-2.0 release

- Status: Accepted
- Date: 2026-07-14

## Decision

FlowBaton source and completed releases are public under Apache-2.0. A release
is complete only after its required source, assets, install paths, and product
documents are anonymously retrievable and pass their integrity checks.

## Consequences

- Private artifact delivery does not complete the v1 goal.
- Release automation uses least-privilege credentials and immutable action
  pins.
- Archives include the project license, notice, README, and support matrix.
- The release gate requires signed artifacts, checksums, an SBOM, and a build
  attestation.
- Commercial products, hosted services, billing, and telemetry remain outside
  this repository.
