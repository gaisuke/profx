package models

import (
	"database/sql"
	"time"
)

type JobStatus string

const (
	JobStatusQueued     JobStatus = "queued"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

type EvaluationJob struct {
	ID               string          `json:"id" db:"id"`
	JobTitle         string          `json:"job_title" db:"job_title"`
	CVDocumentID     string          `json:"cv_document_id" db:"cv_document_id"`
	ReportDocumentID string          `json:"report_document_id" db:"report_document_id"`
	Status           JobStatus       `json:"status" db:"status"`
	CVMatchRate      sql.NullFloat64 `json:"cv_match_rate,omitempty" db:"cv_match_rate"`
	CVFeedback       sql.NullString  `json:"cv_feedback,omitempty" db:"cv_feedback"`
	ProjectScore     sql.NullFloat64 `json:"project_score,omitempty" db:"project_score"`
	ProjectFeedback  sql.NullString  `json:"project_feedback,omitempty" db:"project_feedback"`
	OverallSummary   sql.NullString  `json:"overall_summary,omitempty" db:"overall_summary"`
	ErrorMessage     sql.NullString  `json:"error_message,omitempty" db:"error_message"`
	RetryCount       int             `json:"retry_count" db:"retry_count"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
	CompletedAt      sql.NullTime    `json:"completed_at,omitempty" db:"completed_at"`
}

type EvaluationJobResponse struct {
	ID              string    `json:"id"`
	Status          JobStatus `json:"status"`
	CVMatchRate     *float64  `json:"cv_match_rate,omitempty"`
	CVFeedback      *string   `json:"cv_feedback,omitempty"`
	ProjectScore    *float64  `json:"project_score,omitempty"`
	ProjectFeedback *string   `json:"project_feedback,omitempty"`
	OverallSummary  *string   `json:"overall_summary,omitempty"`
	ErrorMessage    *string   `json:"error_message,omitempty"`
}

func (j *EvaluationJob) ToResponse() *EvaluationJobResponse {
	resp := &EvaluationJobResponse{
		ID:     j.ID,
		Status: j.Status,
	}
	if j.CVMatchRate.Valid {
		resp.CVMatchRate = &j.CVMatchRate.Float64
	}
	if j.CVFeedback.Valid {
		resp.CVFeedback = &j.CVFeedback.String
	}
	if j.ProjectScore.Valid {
		resp.ProjectScore = &j.ProjectScore.Float64
	}
	if j.ProjectFeedback.Valid {
		resp.ProjectFeedback = &j.ProjectFeedback.String
	}
	if j.OverallSummary.Valid {
		resp.OverallSummary = &j.OverallSummary.String
	}
	if j.ErrorMessage.Valid {
		resp.ErrorMessage = &j.ErrorMessage.String
	}
	return resp
}
