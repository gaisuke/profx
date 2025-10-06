DROP TRIGGER IF EXISTS update_evaluation_jobs_updated_at ON evaluation_jobs;
DROP FUNCTION IF EXISTS update_updated_at_column();
DROP TABLE IF EXISTS evaluation_jobs;
DROP TYPE IF EXISTS job_status;