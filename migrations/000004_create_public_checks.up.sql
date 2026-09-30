-- Public CV check (/cek): the self-service side of profx.
--
-- What is deliberately NOT stored: the CV text and the job description. The
-- model reads them, the result is saved, the inputs are dropped. Keeping the
-- inputs would make this table a CV archive, which is exactly what the page
-- promises it is not.
--
-- ip_hash is a salted SHA-256 of the client IP, used only to count free checks
-- per visitor per day. The raw IP is never written.
CREATE TABLE IF NOT EXISTS public_checks (
    id          TEXT PRIMARY KEY,
    job_title   TEXT NOT NULL,
    score       INTEGER NOT NULL CHECK (score >= 0 AND score <= 100),
    payload     JSONB NOT NULL,
    ip_hash     TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

-- Rate limiting counts rows for one ip_hash since midnight WIB.
CREATE INDEX IF NOT EXISTS public_checks_ip_created_idx
    ON public_checks (ip_hash, created_at DESC);

-- The same index shape answers the global daily cap.
CREATE INDEX IF NOT EXISTS public_checks_created_idx
    ON public_checks (created_at DESC);

-- Interest list for the paid, not-yet-active part. Honest demand measurement
-- instead of a fake "beli sekarang" button.
CREATE TABLE IF NOT EXISTS public_check_interest (
    id         TEXT PRIMARY KEY,
    check_id   TEXT,
    contact    TEXT NOT NULL,
    note       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
