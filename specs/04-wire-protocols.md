# Wire Protocol Specification

## 1. Android agent

The Android agent serves the RPC contract declared by the tracked protocol file
and `contracts/v0/android-grpc.json`. Traffic stays on an ADB-forwarded loopback
port; the default device-side port is 7001.

The host uses plaintext HTTP/2 because the transport never leaves the local ADB
tunnel. Each request carries one encoded message and validates the response
frame, status trailers, and content type. On an `UNAVAILABLE` status, the RPC
client returns immediately and performs no internal retry.

The client configures keepalive and a finite connect timeout. Session cleanup
removes only the ADB forward created by that session.

## 2. Android protocol generation

Generated Java and Go shapes must come from the tracked protocol file. Any
service, method, field, or enum change requires a contract-test update in the
same slice. Generated code must not be hand-edited.

## 3. iOS runner

The XCTest runner exposes the routes in `contracts/v0/ios-http.json` over
loopback HTTP. The default port is 22087; managed sessions may select a separate
port for each shard.

Requests and responses use JSON except screenshot bytes. A structured error body
wins over the HTTP status fallback. Invalid input maps to a precondition error,
request expiry maps to a timeout, and runner failures map to an internal error.

## 4. Readiness and shutdown

Managed hosts wait for an explicit readiness response before device commands.
Startup has a finite deadline and includes child-process output in the final
error. Shutdown is idempotent and closes listeners before terminating an owned
child process.

## 5. Hierarchy payloads

Hierarchy nodes carry visible text, identifiers, accessibility text, state,
bounds, and child relationships. Platform adapters normalize those fields into
the shared device model. Missing fields stay absent; adapters do not invent
values.

## 6. Contract discipline

Tracked JSON contracts and protocol files are frozen by tests. Change both the
declaration and every affected adapter in one reviewed commit. Network tests use
loopback servers and finite deadlines.
