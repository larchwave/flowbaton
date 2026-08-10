ALTER TABLE flowbaton_nodes
    ADD COLUMN IF NOT EXISTS worker_epoch bigint NOT NULL DEFAULT 1 CHECK (worker_epoch > 0);

CREATE TABLE IF NOT EXISTS flowbaton_input_jobs (
    session_id text NOT NULL __FK_TARGET__ flowbaton_sessions(session_id) ON DELETE CASCADE,
    request_sequence bigint NOT NULL,
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    owner_node_id text NOT NULL __FK_TARGET__ flowbaton_nodes(node_id),
    request_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    lease_generation bigint NOT NULL CHECK (lease_generation > 0),
    fencing_token_sha256 text NOT NULL,
    stream_epoch bigint NOT NULL CHECK (stream_epoch > 0),
    frame_sequence bigint NOT NULL CHECK (frame_sequence > 0),
    command text NOT NULL CHECK (command IN ('tap','input-text','press-key','swipe','set-orientation')),
    command_payload jsonb NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','claimed','executing','done')),
    claimed_by text __FK_TARGET__ flowbaton_nodes(node_id),
    claim_expires_at timestamptz,
    started_at timestamptz,
    completed_event_sequence bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (session_id, request_sequence),
    UNIQUE (tenant_id, principal_id, idempotency_key),
    FOREIGN KEY (session_id, request_sequence) __FK_TARGET__ flowbaton_requests(session_id, sequence),
    FOREIGN KEY (session_id, completed_event_sequence) __FK_TARGET__ flowbaton_events(session_id, sequence)
);

CREATE INDEX IF NOT EXISTS flowbaton_input_jobs_owner_ready
    ON flowbaton_input_jobs(owner_node_id, state, claim_expires_at, created_at);

CREATE TABLE IF NOT EXISTS flowbaton_frame_jobs (
    session_id text PRIMARY KEY __FK_TARGET__ flowbaton_sessions(session_id) ON DELETE CASCADE,
    owner_node_id text NOT NULL __FK_TARGET__ flowbaton_nodes(node_id),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','claimed')),
    claimed_by text __FK_TARGET__ flowbaton_nodes(node_id),
    claim_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX IF NOT EXISTS flowbaton_frame_jobs_owner_ready
    ON flowbaton_frame_jobs(owner_node_id, state, claim_expires_at, created_at);
