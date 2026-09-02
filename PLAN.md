# Roadmap

## v1

A signed Apache-2.0 release that discovers, validates, and runs YAML flows on
Android devices and iOS Simulators.

That tag also needs green Go, Android, and iOS checks; attested archives;
checksums; an SBOM; anonymous download of every advertised surface; and
unsupported features failing before device mutation. The full gate is
[docs/release-policy.md](docs/release-policy.md).

## After v1

- Physical iOS. The driver is already in this tree; hardware evidence is
  still required.
- Hardening of `explore` and `flowbaton serve`.

Hosted accounts, billing, telemetry, and remote render services stay out of
this repository.

## Now

FlowBaton is pre-alpha. Android and iOS Simulator execution work. Physical
iOS, explore, and the multi-node runtime are in this tree and still
hardening. Homebrew v0.1.1 predates some of that; a source build tracks
`main`.
