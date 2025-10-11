package services

import (
	"fmt"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/storage"
	"github.com/google/uuid"
)

type JobService struct {
	jobRepo      *storage.JobRepository
	documentRepo *storage.DocumentRepository
	jobQueue     chan string
}

func NewJobService(jobRepo *storage.JobRepository, documentRepo *storage.DocumentRepository, jobQueue chan string) *JobService {
	return &JobService{
		jobRepo:      jobRepo,
		documentRepo: documentRepo,
		jobQueue:     jobQueue,
	}
}

func (js *JobService) CreateJob(jobTitle, cvDocID, reportDocID string) (*models.EvaluationJob, error) {
	if _, err := js.documentRepo.GetDocumentByID(cvDocID); err != nil {
		return nil, fmt.Errorf("CV document not found: %w", err)
	}

	if _, err := js.documentRepo.GetDocumentByID(reportDocID); err != nil {
		return nil, fmt.Errorf("project report document not found: %w", err)
	}

	job := &models.EvaluationJob{
		ID:               uuid.New().String(),
		JobTitle:         jobTitle,
		CVDocumentID:     cvDocID,
		ReportDocumentID: reportDocID,
		Status:           models.JobStatusQueued,
		RetryCount:       0,
	}

	if err := js.jobRepo.Create(job); err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	select {
	case js.jobQueue <- job.ID:
	default:
		js.jobRepo.UpdateError(job.ID, "job queue is full", 0)
		return nil, fmt.Errorf("job queue is full, please try again later")
	}

	return job, nil
}

func (js *JobService) GetJobByID(id string) (*models.EvaluationJob, error) {
	return js.jobRepo.GetByID(id)
}

func (js *JobService) UpdateJobError(id, errorMsg string, retryCount int) error {
	return js.jobRepo.UpdateError(id, errorMsg, retryCount)
}

func (js *JobService) IncrementRetryCount(id string) error {
	return js.jobRepo.IncrementRetry(id)
}
