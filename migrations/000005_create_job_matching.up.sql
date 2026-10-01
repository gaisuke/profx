-- Job matching (/cari): public postings, one run per candidate, ranked results.
--
-- Privacy, same rule as /cek: the CV text is never stored. A run keeps only a
-- fingerprint of the CV (so scores can be reused across runs without keeping the
-- CV itself) plus the verdicts, and the verdicts expire.
CREATE TABLE IF NOT EXISTS job_postings (
    id           TEXT PRIMARY KEY,           -- hash of source + external id
    source       TEXT NOT NULL,
    external_id  TEXT NOT NULL,
    title        TEXT NOT NULL,
    company      TEXT,
    location     TEXT,
    remote       BOOLEAN NOT NULL DEFAULT false,
    url          TEXT NOT NULL,
    description  TEXT,
    tags         TEXT[],
    published_at TIMESTAMPTZ,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);

CREATE INDEX IF NOT EXISTS job_postings_published_idx ON job_postings (published_at DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS job_postings_source_idx ON job_postings (source);

CREATE TABLE IF NOT EXISTS match_runs (
    id             TEXT PRIMARY KEY,
    cv_fingerprint TEXT NOT NULL,
    status         TEXT NOT NULL,            -- jalan | selesai | gagal
    pesan          TEXT,
    total          INTEGER NOT NULL DEFAULT 0,
    selesai        INTEGER NOT NULL DEFAULT 0,
    filters        JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip_hash        TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS match_runs_ip_created_idx ON match_runs (ip_hash, created_at DESC);
CREATE INDEX IF NOT EXISTS match_runs_created_idx ON match_runs (created_at DESC);

CREATE TABLE IF NOT EXISTS match_results (
    run_id     TEXT NOT NULL REFERENCES match_runs(id) ON DELETE CASCADE,
    job_id     TEXT NOT NULL,
    score      INTEGER NOT NULL,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, job_id)
);

-- Re-scoring the same CV against the same posting is wasted money. The key is a
-- hash of the CV, so the cache works without ever storing the CV.
CREATE TABLE IF NOT EXISTS job_scores (
    cv_fingerprint TEXT NOT NULL,
    job_id         TEXT NOT NULL,
    score          INTEGER NOT NULL,
    payload        JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cv_fingerprint, job_id)
);
