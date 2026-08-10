# FlowBaton Integration Contract v1

This public contract is the only supported boundary between FlowBaton and a
separate process such as Studio Core. A proprietary consumer must not import Go
packages from `internal/...`, link private product behavior into FlowBaton, or
make FlowBaton depend on the consumer at build or runtime.

`schema.json` defines the identity and capability handshake emitted before any
project, build, or device mutation. It binds the executable version and SHA-256,
the Apache-2.0 license, the supported transports, authentication profiles, and
the exact versions of the flow, DeviceSession, and report contracts.

CLI/stdio and MCP retain their native invocation security. Authenticated local
IPC requires OS peer identity, a per-launch token, endpoint channel binding,
nonce, and expiry. Authenticated remote IPC requires mutual TLS, a signed
short-lived session token, TLS-exporter channel binding, explicit tenant and
principal scope, nonce/expiry, and lease-generation replay protection. A bearer
token without channel binding or a caller-supplied tenant ID is insufficient.

All transports preserve the same command, capability, error, and evidence
semantics. A remote service must derive tenant and principal from authenticated
identity and reject cross-tenant resource IDs before acquisition. Unknown
contract/auth versions, identity or digest mismatch, stale/replayed nonce, or an
unsupported capability must fail closed before mutation.

JSON Schema closes each advertised profile shape. Cross-array uniqueness and the rule
that every advertised authenticated transport has exactly one matching profile are
normative semantic checks implemented by `ValidateJSON` in this directory; schema
validation alone is insufficient.

Compatibility policy is N/N-1 within a supported major contract line. Consumers
must display the negotiated FlowBaton version and digest and keep bundled OSS
notices. FlowBaton remains fully usable without any proprietary consumer.

The Go package exports `Document`, its component types, normative authentication
profiles, and `NewDocument`. Runtime producers must construct the handshake with
`NewDocument`; it performs the same semantic validation as `ValidateJSON` before
the document can be emitted.
