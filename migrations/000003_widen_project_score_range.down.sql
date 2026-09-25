ALTER TABLE evaluation_jobs DROP CONSTRAINT IF EXISTS project_score_range;

ALTER TABLE evaluation_jobs
    ADD CONSTRAINT project_score_range CHECK (project_score >= 0.00 AND project_score <= 1.00);
