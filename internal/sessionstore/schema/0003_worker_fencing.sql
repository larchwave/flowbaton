ALTER TABLE flowbaton_nodes
    ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS ready boolean NOT NULL DEFAULT true;

UPDATE flowbaton_nodes
SET lease_expires_at = last_heartbeat_at
WHERE lease_expires_at IS NULL;

ALTER TABLE flowbaton_nodes
    ALTER COLUMN lease_expires_at SET NOT NULL;

ALTER TABLE flowbaton_devices
    ADD COLUMN IF NOT EXISTS owner_worker_epoch bigint NOT NULL DEFAULT 1 CHECK (owner_worker_epoch > 0);

ALTER TABLE flowbaton_sessions
    ADD COLUMN IF NOT EXISTS owner_worker_epoch bigint NOT NULL DEFAULT 1 CHECK (owner_worker_epoch > 0);

UPDATE flowbaton_devices d
SET owner_worker_epoch = n.worker_epoch
FROM flowbaton_nodes n
WHERE d.owner_node_id = n.node_id;

UPDATE flowbaton_sessions s
SET owner_worker_epoch = n.worker_epoch
FROM flowbaton_nodes n
WHERE s.owner_node_id = n.node_id;

ALTER TABLE flowbaton_input_jobs
    ADD COLUMN IF NOT EXISTS owner_worker_epoch bigint NOT NULL DEFAULT 1 CHECK (owner_worker_epoch > 0),
    ADD COLUMN IF NOT EXISTS claimed_worker_epoch bigint,
    ADD COLUMN IF NOT EXISTS claim_generation bigint NOT NULL DEFAULT 0 CHECK (claim_generation >= 0);

UPDATE flowbaton_input_jobs j
SET owner_worker_epoch = s.owner_worker_epoch
FROM flowbaton_sessions s
WHERE j.session_id = s.session_id;

UPDATE flowbaton_input_jobs j
SET claimed_worker_epoch = n.worker_epoch
FROM flowbaton_nodes n
WHERE j.claimed_by = n.node_id AND j.claimed_worker_epoch IS NULL;

ALTER TABLE flowbaton_input_jobs
    DROP CONSTRAINT IF EXISTS flowbaton_input_jobs_claimed_worker_epoch_check;
ALTER TABLE flowbaton_input_jobs
    ADD CONSTRAINT flowbaton_input_jobs_claimed_worker_epoch_check
    CHECK ((claimed_by IS NULL) = (claimed_worker_epoch IS NULL));

ALTER TABLE flowbaton_frame_jobs
    ADD COLUMN IF NOT EXISTS owner_worker_epoch bigint NOT NULL DEFAULT 1 CHECK (owner_worker_epoch > 0),
    ADD COLUMN IF NOT EXISTS claimed_worker_epoch bigint,
    ADD COLUMN IF NOT EXISTS claim_generation bigint NOT NULL DEFAULT 0 CHECK (claim_generation >= 0);

UPDATE flowbaton_frame_jobs j
SET owner_worker_epoch = s.owner_worker_epoch
FROM flowbaton_sessions s
WHERE j.session_id = s.session_id;

UPDATE flowbaton_frame_jobs j
SET claimed_worker_epoch = n.worker_epoch
FROM flowbaton_nodes n
WHERE j.claimed_by = n.node_id AND j.claimed_worker_epoch IS NULL;

ALTER TABLE flowbaton_frame_jobs
    DROP CONSTRAINT IF EXISTS flowbaton_frame_jobs_claimed_worker_epoch_check;
ALTER TABLE flowbaton_frame_jobs
    ADD CONSTRAINT flowbaton_frame_jobs_claimed_worker_epoch_check
    CHECK ((claimed_by IS NULL) = (claimed_worker_epoch IS NULL));
