package storage

import (
	"database/sql"
	"fmt"

	"github.com/gaisuke/profx/internal/models"
)

type JobRepository struct {
	db *sql.DB
}

func NewJobRepository(db *sql.DB) *JobRepository {
	return &JobRepository{
		db: db,
	}
}

func (r *JobRepository) Create(job *models.EvaluationJob) error {
	query := `
		INSERT INTO evaluation_jobs (
			id, job_title, cv_document_id, report_document_id, status,
			retry_count, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRow(
		query,
		job.ID, job.JobTitle, job.CVDocumentID, job.ReportDocumentID,
		job.Status, job.RetryCount,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

func (r *JobRepository) GetByID(id string) (*models.EvaluationJob, error) {
	// select all fields from evaluation_jobs where id = $1
	query := `
		SELECT id, job_title, cv_document_id, report_document_id, status,
			cv_match_rate, cv_feedback, project_score, project_feedback,
			overall_summary, error_message, retry_count,
			created_at, updated_at, completed_at
		FROM evaluation_jobs
		WHERE id = $1
	`

	job := &models.EvaluationJob{}
	err := r.db.QueryRow(query, id).Scan(
		&job.ID, &job.JobTitle, &job.CVDocumentID, &job.ReportDocumentID, &job.Status,
		&job.CVMatchRate, &job.CVFeedback, &job.ProjectScore, &job.ProjectFeedback,
		&job.OverallSummary, &job.ErrorMessage, &job.RetryCount,
		&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found: %s", id)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get job by ID: %w", err)
	}

	return job, nil
}

func (r *JobRepository) UpdateStatus(job *models.EvaluationJob) error {
	query := `
		UPDATE evaluation_jobs
		SET status = $1, updated_at = NOW()
		WHERE id = $2
	`

	result, err := r.db.Exec(query, job.Status, job.ID)
	if err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no job found with ID: %s", job.ID)
	}

	return nil
}

func (r *JobRepository) UpdateResult(job *models.EvaluationJob) error {
	query := `
		UPDATE evaluation_jobs
		SET 
			status = $1,
			cv_match_rate = $2,
			cv_feedback = $3,
			project_score = $4,
			project_feedback = $5,
			overall_summary = $6,
			completed_at = NOW(),
			updated_at = NOW()
		WHERE id = $7
	`

	result, err := r.db.Exec(
		query,
		job.Status,
		job.CVMatchRate,
		job.CVFeedback,
		job.ProjectScore,
		job.ProjectFeedback,
		job.OverallSummary,
		job.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update job result: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no job found with ID: %s", job.ID)
	}

	return nil
}

func (r *JobRepository) UpdateError(id, errorMsg string, retryCount int) error {
	query := `
		UPDATE evaluation_jobs
		SET 
			status = $1,
			error_message = $2,
			retry_count = $3,
			updated_at = NOW()
		WHERE id = $4
	`

	result, err := r.db.Exec(
		query,
		models.JobStatusFailed,
		errorMsg,
		retryCount,
		id,
	)

	if err != nil {
		return fmt.Errorf("failed to update job error: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no job found with ID: %s", id)
	}

	return nil
}

func (r *JobRepository) IncrementRetry(id string) error {
	query := `
		UPDATE evaluation_jobs
		SET retry_count = retry_count + 1, updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to increment retry count: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no job found with ID: %s", id)
	}

	return nil
}
