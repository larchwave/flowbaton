# DeviceSession Contract v1

DeviceSession v1 is a transport-neutral lifecycle contract above the platform
Driver. It supplies the acquisition, ownership, transport, and recovery boundary
that the declarations-only `internal/device.Driver` intentionally does not own.
Local IPC and authenticated remote cloud transports preserve identical lease,
ordering, cancellation, error, and evidence semantics.

Before acquisition, the transport authenticates a principal and derives its
tenant. The session binds that identity to a channel-binding digest, auth profile,
short-lived nonce, and expiry. The server must never trust a caller-supplied tenant
without matching authenticated identity; cross-tenant resource lookup fails before
device mutation.

The typed request plane covers acquire, input, heartbeat/renew, reconnect, cancel,
and release. Every request repeats its authenticated tenant/principal/channel binding;
every post-acquire request and event carries the current lease generation and
fencing-token digest. Reacquisition increments generation;
stale generation or fencing material returns `FENCED`/`STALE_LEASE`, including after
server restart. Release and cancellation use stable idempotency keys, and exactly one
terminal `released` event is recorded.

Events and requests have monotonic RFC 3339 timestamps, are uniquely identified, and
are closed by type. Frame events carry
stream epoch, frame sequence, orientation, dimensions, content identity, queue depth,
and dropped-frame count. Inputs and acknowledgements bind the exact
`stream_epoch + frame_sequence` observed before the request, so reconnect cannot alias
an old frame. A heartbeat result binds its request ID/idempotency key and may extend the
lease only by the requested duration and never past authenticated binding expiry.
Producers apply bounded queues and explicit backpressure; they
may drop declared frames but never silently drop control or terminal events.

Disconnect does not imply release. Reconnect is legal only while the same fenced lease
is live and resumes from the last acknowledged server sequence with a new stream epoch.
Remote reconnect uses a fresh channel-bound token on a different TLS channel, with a new
nonce and expiry. It atomically rotates the channel digest, nonce, and binding expiry while
tenant, principal, lease generation, and fence remain fixed; the prior token identity
cannot continue the session. A newly issued token on the same channel does not replace the
active binding outside reconnect. Semantic verification requires the authenticated token's
expiry to equal the final transcript binding expiry exactly.
Cancellation is idempotent before release and repeated requests return the first terminal
outcome. After release, only an exact retry of the release idempotency key and payload may
read that same outcome; every other request is rejected without mutation.
Authentication, authorization, tenant, lease, capability, backpressure, transport,
device, and transition failures use the enumerated typed errors and never fall back to
an unscoped session.

Frame bytes are available through a bounded authenticated content plane keyed by
session ID, stream epoch, frame sequence, and SHA-256. Access also requires the current
tenant, principal, channel binding, token nonce, token expiry, generation, and fence.
Only the latest frame content is retained for a live session; terminal release removes
it. A failed frame capture is retried a bounded number of times and then blocks later
input instead of permitting use of stale frame state.

PostgreSQL time is authoritative for token validity, lease, binding, claim, request,
event, and expiry decisions. Token issuance reserves a database-derived issue/expiry
window, and authenticated requests are checked against database time. Automatic expiry
records the release request and its replay record with the lease's predeclared release
key, resolves unfinished jobs as non-retryable, clears the device lease, and emits the
one terminal `released` event with the `error` outcome. Cancellation resolves queued
work and signals an executing operation; once physical input has started, interruption
is recorded as an unknown, non-retryable outcome.

JSON Schema closes each request/event payload. Cross-document equalities, authenticated
context, time validity, lifecycle ordering, frame links, reconnect cursors, and
lease fencing are normative semantic rules implemented by `ValidateJSON` in this
directory. Consumers must run equivalent checks before acquisition or mutation; schema
validation alone is insufficient.

The Go package exports the document, binding, lease, request, and event types,
plus constructors for validated transcripts and strictly encoded request/event
payloads. The PostgreSQL runtime integration suite uses a disposable database
from `FLOWBATON_TEST_POSTGRES_URL`; it skips only when that variable is absent.

The runtime binds each node process to a database-issued epoch and readiness
lease. Device ownership, frame claims, input claims, starts, completions, and
restart recovery carry that epoch plus a monotonic claim number. An input that
reached `executing` on an older epoch receives one unknown-outcome event and is
never queued for another device call.
