CREATE TYPE job_status AS ENUM (
    'queued',
    'processing',
    'completed',
    'failed'
);

CREATE TABLE evaluation_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_title VARCHAR(255) NOT NULL,
    cv_document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    report_document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    status job_status NOT NULL DEFAULT 'queued',
    cv_match_rate DECIMAL(3,2),
    cv_feedback TEXT,
    project_score DECIMAL(3,2),
    project_feedback TEXT,
    overall_summary TEXT,
    error_message TEXT,
    retry_count INT DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    CONSTRAINT cv_match_rate_range CHECK (cv_match_rate >= 0.00 AND cv_match_rate <= 1.00),
    CONSTRAINT project_score_range CHECK (project_score >= 0.00 AND project_score <= 1.00)
);

CREATE INDEX idx_evaluation_jobs_status ON evaluation_jobs(status);
CREATE INDEX idx_evaluation_jobs_created_at ON evaluation_jobs(created_at);
CREATE INDEX idx_evaluation_jobs_cv_document_id ON evaluation_jobs(cv_document_id);
CREATE INDEX idx_evaluation_jobs_report_document_id ON evaluation_jobs(report_document_id);

CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_evaluation_jobs_updated_at
    BEFORE UPDATE ON evaluation_jobs
    FOR EACH ROW
    EXECUTE PROCEDURE update_updated_at_column();