# Remote DeviceSession runtime

`flowbaton serve` runs the Integration v1 and DeviceSession v1 HTTPS service.
Local CLI runs do not need this service. It is for operators who need a shared,
authenticated pool of Android devices, iOS Simulators, or web targets.

## Required inputs

Each node needs:

- one PostgreSQL database shared by all nodes;
- a TLS server certificate and private key;
- a client CA whose certificates identify allowed callers;
- one FlowBaton Ed25519 signing key;
- a stable node ID and an HTTPS address advertised to callers;
- a strict JSON inventory of devices attached to that node; and
- the platform SDKs and targets required by those inventory entries.

On Unix systems, the TLS private key and FlowBaton signing key must not grant
group or world permissions. Key and certificate paths must be regular files,
not symbolic links. TLS is fixed to version 1.3 with client certificates.

## Prepare the database and signing key

```sh
export FLOWBATON_DATABASE_URL='postgres://flowbaton@db.example/flowbaton?sslmode=verify-full'

flowbaton db apply-schema --database-url "$FLOWBATON_DATABASE_URL"
flowbaton auth keygen \
  --key-id runtime-key-2026-08 \
  --private-key runtime-signing.json \
  --public-key runtime-signing-public.json
chmod 600 runtime-signing.json server-key.pem
```

Map each client certificate to the tenant and principal that the server will
place in its short-lived session tokens. Compute the lowercase SHA-256 digest
of the DER certificate, then store it:

```sh
CLIENT_CERT_SHA256="$(
  openssl x509 -in client-cert.pem -outform DER |
    openssl dgst -sha256 -binary |
    xxd -p -c 256
)"

flowbaton auth cert-map add \
  --database-url "$FLOWBATON_DATABASE_URL" \
  "$CLIENT_CERT_SHA256" tenant-1 automation-1
```

Mappings can be listed or revoked with `flowbaton auth cert-map list` and
`flowbaton auth cert-map revoke`.

## Device inventory

The inventory accepts 1 through 256 entries and at most five capabilities per
entry. IDs are bounded. Duplicate resource IDs, duplicate capabilities, unknown
fields, and commands the selected driver cannot execute are rejected before a
driver opens.

```json
{
  "devices": [
    {
      "tenant_id": "tenant-1",
      "resource_id": "android-checkout-1",
      "platform": "android",
      "device": "emulator-5554",
      "port": 7001,
      "reinstall_driver": true,
      "capabilities": ["tap", "input-text", "press-key", "swipe", "set-orientation"]
    }
  ]
}
```

Keep a resource ID bound to the same physical target across restarts. A node ID
identifies one configured host role and must also remain stable. Running two
live processes with the same node ID is rejected.

## Start a node

```sh
flowbaton serve \
  --address 0.0.0.0:7443 \
  --public-address https://node-1.example:7443 \
  --database-url "$FLOWBATON_DATABASE_URL" \
  --tls-cert server-cert.pem \
  --tls-key server-key.pem \
  --client-ca client-ca.pem \
  --signing-key runtime-signing.json \
  --signing-key-id runtime-key-2026-08 \
  --node-id node-1 \
  --inventory inventory.json
```

The process publishes `GET /v1/integration` before opening any driver. During
driver startup, `GET /health/live` succeeds and `GET /health/ready` returns 503.
Readiness changes to 200 only after every configured driver opens and the node
is activated in PostgreSQL.

The service also exposes:

- `POST /v1/session-tokens` for channel-bound FlowBaton tokens;
- `POST /v1/device-sessions` for acquisition;
- `POST /v1/device-sessions/{sessionID}/requests` for lifecycle and input
  requests;
- `GET /v1/device-sessions/{sessionID}/events` for ordered NDJSON events; and
- `GET /v1/device-sessions/{sessionID}/frames/{streamEpoch}/{frameSequence}`
  for the authenticated image named by a frame event.

All endpoints require a client certificate at the TLS layer. Session endpoints
also require a `FlowBaton` authorization token bound to that certificate and
TLS channel. Tenant and principal values come from the certificate mapping,
not request JSON.

Event-stream requests must send `X-FlowBaton-Generation` and
`X-FlowBaton-Fence`. Frame downloads send those headers plus
`X-FlowBaton-Content-SHA256` from the frame event. The server checks the
tenant, principal, current TLS binding, token nonce, token expiry, generation,
fence, stream epoch, sequence, digest, lease, and device ownership before
returning bytes. A frame is at most 16 MiB, is served as decoder-confirmed PNG
or JPEG, and is removed when a newer frame is committed or the session ends.

Each TLS connection obtains its own short-lived token. After transport loss,
the client opens a new mutually authenticated TLS connection, obtains a fresh
token with a new nonce and expiry, and sends `reconnect`. PostgreSQL requires
all three token-binding values to differ, then atomically rotates the stored
channel binding, nonce, and token expiry while preserving tenant, principal,
generation, and fence. Requests carrying any prior binding value then fail.
Issuing another token on the same TLS channel does not extend or replace an
active session binding; that token becomes usable for the session only through
the reconnect transition after transport loss. Transcript validation requires
the authenticated token expiry to equal the final stored binding expiry.

## Fencing and restart behavior

PostgreSQL grants each process incarnation a node epoch and is the sole clock
for token validity, node leases, session leases, claims, starts, completions,
expiry, requests, and events. Token issuance atomically reserves a nonce and a
database-derived whole-second issue/expiry window. The server signs that exact
window, and every protected request validates it against a fresh database
timestamp. The node heartbeat runs independently of device workers and each
database call has a bounded context. A process that loses its epoch stops the
listener and workers; its later claim, start, and completion writes are refused.

Restart registration changes ownership only for devices present in that
process's inventory. Omitted devices stay fenced and unavailable. Before a
configured driver opens, the new process waits one full device-operation
window for any older executing call, then records a non-retryable unknown
outcome. This quarantine is the boundary between an older process and new
physical device work.

Frame and input claims carry both the node epoch and a monotonic claim number.
Pending work can be claimed again after an expired claim. Input work is marked
`executing` before the device call. If a process stops after that point, the
next incarnation records one non-retryable unknown-outcome event and never
places that input back on the queue.

Cancel marks the session first, completes queued inputs with non-retryable
typed errors, and signals an executing worker through durable PostgreSQL
state. If cancellation or timeout happens after `executing`, the result is
recorded as an unknown, non-retryable outcome. A fresh frame is required after
every completed input, including a rejected input. Frame capture retries at
most three times; a terminal capture failure leaves the frame job blocked, so
no later input can use stale pixels.

When a lease or token binding expires, the server creates the terminal release
request using the session's predeclared release key, closes outstanding work,
deletes frame bytes, clears device ownership, and emits the single `released`
event with the `error` outcome. This also wakes a waiting event stream. Control
and terminal events are not limited by prior event count. Queued input and live
token nonces have explicit pressure limits; excess work returns
`BACKPRESSURE_LIMIT`.

Defaults are a five-second node heartbeat, a 30-second work claim, and a
20-second device-operation timeout. The node lease spans three heartbeat
intervals. `--worker-claim` must exceed `--worker-timeout` by at least five
seconds. Worker timeouts are capped at ten minutes; heartbeat intervals must be
from 100 milliseconds through one minute.

On controlled shutdown the process stops accepting work, deactivates its node
with a fresh bounded context, then closes drivers in reverse order. A later
process with the same node ID can start immediately after deactivation. After
an abrupt stop it must wait for the database lease to expire.

PostgreSQL availability, backup, restore, certificate issuance, key custody,
and network access control remain operator responsibilities. A green local test
does not establish those production properties.

Web log and crash capture remain unsupported and fail closed. Android and iOS
diagnostic methods are implemented, but a release operator must still prove
them on the exact connected targets used for a candidate release.
