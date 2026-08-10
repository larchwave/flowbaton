ALTER TABLE flowbaton_frame_jobs
    DROP CONSTRAINT IF EXISTS flowbaton_frame_jobs_state_check;
ALTER TABLE flowbaton_frame_jobs
    ADD CONSTRAINT flowbaton_frame_jobs_state_check
    CHECK (state IN ('pending','claimed','blocked'));

CREATE TABLE IF NOT EXISTS flowbaton_frame_content (
    session_id text NOT NULL __FK_TARGET__ flowbaton_sessions(session_id) ON DELETE CASCADE,
    stream_epoch bigint NOT NULL CHECK (stream_epoch > 0),
    frame_sequence bigint NOT NULL CHECK (frame_sequence > 0),
    content_sha256 text NOT NULL,
    content_type text NOT NULL CHECK (content_type IN ('image/png','image/jpeg')),
    content bytea NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (session_id, stream_epoch, frame_sequence),
    UNIQUE (session_id, content_sha256)
);

CREATE INDEX IF NOT EXISTS flowbaton_frame_content_expiry
    ON flowbaton_frame_content(expires_at);
