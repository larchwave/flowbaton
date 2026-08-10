CREATE TABLE IF NOT EXISTS flowbaton_schema_versions (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS flowbaton_nodes (
    node_id text PRIMARY KEY,
    public_address text NOT NULL,
    last_heartbeat_at timestamptz NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS flowbaton_devices (
    tenant_id text NOT NULL,
    resource_id text NOT NULL,
    owner_node_id text __FK_TARGET__ flowbaton_nodes(node_id),
    capabilities jsonb NOT NULL DEFAULT '[]'::jsonb,
    lease_generation bigint NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    current_session_id text,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, resource_id)
);

CREATE TABLE IF NOT EXISTS flowbaton_identity_mappings (
    certificate_fingerprint_sha256 text PRIMARY KEY,
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS flowbaton_token_nonces (
    certificate_fingerprint_sha256 text NOT NULL __FK_TARGET__ flowbaton_identity_mappings(certificate_fingerprint_sha256) ON DELETE CASCADE,
    nonce text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (certificate_fingerprint_sha256, nonce)
);

CREATE TABLE IF NOT EXISTS flowbaton_sessions (
    session_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    auth_profile_id text NOT NULL,
    channel_binding_sha256 text NOT NULL,
    request_nonce text NOT NULL,
    binding_expires_at timestamptz NOT NULL,
    resource_id text NOT NULL,
    owner_node_id text __FK_TARGET__ flowbaton_nodes(node_id),
    lease_id text NOT NULL UNIQUE,
    lease_generation bigint NOT NULL CHECK (lease_generation > 0),
    fencing_token_sha256 text NOT NULL,
    release_idempotency_key text NOT NULL,
    capabilities jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'disconnected', 'cancelled', 'released')),
    stream_epoch bigint NOT NULL DEFAULT 1 CHECK (stream_epoch > 0),
    acquired_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    heartbeat_interval_ms bigint NOT NULL CHECK (heartbeat_interval_ms > 0),
    released_at timestamptz,
    UNIQUE (tenant_id, resource_id, lease_generation)
);

ALTER TABLE flowbaton_devices
    DROP CONSTRAINT IF EXISTS flowbaton_devices_current_session_fk;
ALTER TABLE flowbaton_devices
    ADD CONSTRAINT flowbaton_devices_current_session_fk
    FOREIGN KEY (current_session_id) __FK_TARGET__ flowbaton_sessions(session_id);

CREATE TABLE IF NOT EXISTS flowbaton_requests (
    session_id text NOT NULL __FK_TARGET__ flowbaton_sessions(session_id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    request_id text NOT NULL,
    type text NOT NULL CHECK (type IN ('acquire', 'input', 'heartbeat', 'reconnect', 'cancel', 'release')),
    idempotency_key text NOT NULL,
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    channel_binding_sha256 text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (session_id, sequence),
    UNIQUE (session_id, request_id)
);

CREATE TABLE IF NOT EXISTS flowbaton_events (
    session_id text NOT NULL __FK_TARGET__ flowbaton_sessions(session_id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    event_id text NOT NULL,
    type text NOT NULL CHECK (type IN ('acquired', 'frame', 'input_ack', 'heartbeat', 'disconnected', 'reconnected', 'cancelled', 'released', 'error')),
    lease_generation bigint NOT NULL CHECK (lease_generation > 0),
    fencing_token_sha256 text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (session_id, sequence),
    UNIQUE (session_id, event_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS flowbaton_one_release_event
    ON flowbaton_events(session_id) WHERE type = 'released';

CREATE TABLE IF NOT EXISTS flowbaton_idempotency (
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    session_id text NOT NULL __FK_TARGET__ flowbaton_sessions(session_id) ON DELETE CASCADE,
    event_sequence bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, principal_id, idempotency_key),
    FOREIGN KEY (session_id, event_sequence) __FK_TARGET__ flowbaton_events(session_id, sequence)
);

CREATE INDEX IF NOT EXISTS flowbaton_events_resume
    ON flowbaton_events(session_id, sequence);
CREATE INDEX IF NOT EXISTS flowbaton_live_leases
    ON flowbaton_sessions(tenant_id, resource_id, lease_expires_at)
    WHERE status IN ('active', 'disconnected', 'cancelled');
