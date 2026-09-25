-- The schema and the application disagreed about the project score scale:
-- this table allowed 0.00-1.00 while the prompt and the validator both use the
-- rubric's own 1-5 scale. The old shared normaliser divided anything above 1 by
-- 100 so a score of 4.2 could be stored as 0.042 — satisfying the constraint and
-- corrupting the value. Widen the range instead of rescaling the score: the
-- stored number now means what the rubric says it means.
ALTER TABLE evaluation_jobs DROP CONSTRAINT IF EXISTS project_score_range;

ALTER TABLE evaluation_jobs
    ADD CONSTRAINT project_score_range CHECK (project_score >= 0.00 AND project_score <= 5.00);
