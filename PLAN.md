# FlowBaton Development Plan

## Product goal

Publish a signed Apache-2.0 v1 that can discover, validate, and execute mobile
UI flows on supported Android devices and iOS Simulators.

## Delivery stages

1. Stabilize the flow parser, execution engine, and device-neutral contracts.
2. Complete Android agent installation, lifecycle, and device operations.
3. Complete iOS runner installation, lifecycle, and simulator operations.
4. Finish CLI reporting, recording, sharding, shell completion, and MCP tools.
5. Harden error handling, cancellation, timeouts, and debug artifacts.
6. Publish signed archives, checksums, an SBOM, install scripts, and public
   documentation.

## Release requirements

- All committed contracts and product specifications agree with the code.
- Go, Android, and iOS checks pass on their supported toolchains.
- The release workflow uses immutable action pins and least-privilege tokens.
- Every archive contains the license, notice, README, and support documents.
- Release assets are downloadable without project credentials.
- Checksums and signatures validate each published archive.
- Unsupported features fail with a clear error before device mutation.

## Current work

The current milestone hardens cross-platform driver packaging, expands device
smoke coverage, and closes the release requirements above.
