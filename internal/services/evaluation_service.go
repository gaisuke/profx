package services

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gaisuke/profx/internal/llm"
	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/storage"
	"github.com/gaisuke/profx/internal/utils"
	"github.com/ledongthuc/pdf"
)

type EvaluationService struct {
	jobRepo      *storage.JobRepository
	documentRepo *storage.DocumentRepository
	ragieClient  Retriever
	llmClient    LLM
}

func NewEvaluationService(
	jobRepo *storage.JobRepository,
	documentRepo *storage.DocumentRepository,
	ragieClient Retriever,
	llmClient LLM,
) *EvaluationService {
	return &EvaluationService{
		jobRepo:      jobRepo,
		documentRepo: documentRepo,
		ragieClient:  ragieClient,
		llmClient:    llmClient,
	}
}

// CVEvaluationResult represents the result of CV evaluation
type CVEvaluationResult struct {
	CVMatchRate float64 `json:"cv_match_rate"`
	CVFeedback  string  `json:"cv_feedback"`
}

// ProjectEvaluationResult represents the result of project evaluation
type ProjectEvaluationResult struct {
	ProjectScore    float64 `json:"project_score"`
	ProjectFeedback string  `json:"project_feedback"`
}

// FinalSummaryResult represents the final summary
type FinalSummaryResult struct {
	OverallSummary string `json:"overall_summary"`
}

// EvaluateJob performs the complete evaluation pipeline for a job
func (es *EvaluationService) EvaluateJob(ctx context.Context, jobID string) error {
	log.Printf("[Job %s] Starting evaluation pipeline", jobID)

	// Get job details
	job, err := es.jobRepo.GetByID(jobID)
	if err != nil {
		return fmt.Errorf("failed to get job: %w", err)
	}

	// Update status to processing
	job.Status = models.JobStatusProcessing
	if err := es.jobRepo.UpdateStatus(job); err != nil {
		log.Printf("[Job %s] Failed to update status to processing: %v", jobID, err)
	}

	// Get documents
	cvDoc, err := es.documentRepo.GetDocumentByID(job.CVDocumentID)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("Failed to get CV document: %v", err), 0)
		return fmt.Errorf("failed to get CV document: %w", err)
	}

	reportDoc, err := es.documentRepo.GetDocumentByID(job.ReportDocumentID)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("Failed to get report document: %v", err), 0)
		return fmt.Errorf("failed to get report document: %w", err)
	}

	// Stage 1: Evaluate CV
	log.Printf("[Job %s] Stage 1: Evaluating CV", jobID)
	cvResult, err := es.evaluateCV(ctx, job.JobTitle, cvDoc.FilePath)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("CV evaluation failed: %v", err), 0)
		return fmt.Errorf("CV evaluation failed: %w", err)
	}

	// Stage 2: Evaluate Project Report
	log.Printf("[Job %s] Stage 2: Evaluating Project Report", jobID)
	projectResult, err := es.evaluateProject(ctx, reportDoc.FilePath)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("Project evaluation failed: %v", err), 0)
		return fmt.Errorf("project evaluation failed: %w", err)
	}

	// Stage 3: Generate Final Summary
	log.Printf("[Job %s] Stage 3: Generating final summary", jobID)
	summary, err := es.generateSummary(ctx, cvResult, projectResult)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("Summary generation failed: %v", err), 0)
		return fmt.Errorf("summary generation failed: %w", err)
	}

	// Update job with results
	job.CVMatchRate = sql.NullFloat64{Float64: utils.NormalizeMatchRate(cvResult.CVMatchRate), Valid: true}
	job.CVFeedback = sql.NullString{String: cvResult.CVFeedback, Valid: true}
	job.ProjectScore = sql.NullFloat64{Float64: utils.NormalizeProjectScore(projectResult.ProjectScore), Valid: true}
	job.ProjectFeedback = sql.NullString{String: projectResult.ProjectFeedback, Valid: true}
	job.OverallSummary = sql.NullString{String: summary.OverallSummary, Valid: true}
	job.Status = models.JobStatusCompleted

	if err := es.jobRepo.UpdateResult(job); err != nil {
		return fmt.Errorf("failed to update job result: %w", err)
	}

	log.Printf("[Job %s] Evaluation completed successfully", jobID)
	return nil
}

// evaluateCV reads the CV from disk and evaluates it. Reading and evaluating are
// separate so the evaluation can be driven from plain text — by unit tests and
// by the eval harness — without fabricating PDF files.
func (es *EvaluationService) evaluateCV(ctx context.Context, jobTitle, cvFilePath string) (*CVEvaluationResult, error) {
	cvContent, err := readPDFContent(cvFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CV: %w", err)
	}
	return es.evaluateCVText(ctx, jobTitle, cvContent)
}

// evaluateCVText runs the CV stage: rubric context, prompt, model call, parse, validate.
func (es *EvaluationService) evaluateCVText(ctx context.Context, jobTitle, cvContent string) (*CVEvaluationResult, error) {
	context, degraded := retrieveWithRetry(func() (string, error) {
		return es.ragieClient.RetrieveForCV(jobTitle)
	}, "CV")
	if degraded {
		log.Printf("Warning: CV rubric unavailable; scoring the CV without rubric context")
	}
	prompt := buildCVEvaluationPrompt(context, cvContent, jobTitle)
	response, err := es.llmClient.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}
	var result CVEvaluationResult
	if err := llm.ParseJSONResponse(response, &result); err != nil {
		return nil, fmt.Errorf("failed to parse CV evaluation response: %w", err)
	}
	if err := validateCVResult(&result); err != nil {
		return nil, fmt.Errorf("invalid CV result: %w (model reply: %s)", err, truncate(response))
	}
	return &result, nil
}

// evaluateProject reads the project report from disk and evaluates it.
func (es *EvaluationService) evaluateProject(ctx context.Context, reportFilePath string) (*ProjectEvaluationResult, error) {
	reportContent, err := readPDFContent(reportFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project report: %w", err)
	}
	return es.evaluateProjectText(ctx, reportContent)
}

// evaluateProjectText runs the project stage: rubric context, prompt, model call, parse, validate.
func (es *EvaluationService) evaluateProjectText(ctx context.Context, reportContent string) (*ProjectEvaluationResult, error) {
	context, degraded := retrieveWithRetry(func() (string, error) {
		return es.ragieClient.RetrieveForProject()
	}, "project")
	if degraded {
		log.Printf("Warning: project rubric unavailable; scoring the report without rubric context")
	}
	prompt := buildProjectEvaluationPrompt(context, reportContent)
	response, err := es.llmClient.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}
	var result ProjectEvaluationResult
	if err := llm.ParseJSONResponse(response, &result); err != nil {
		return nil, fmt.Errorf("failed to parse project evaluation response: %w", err)
	}
	if err := validateProjectResult(&result); err != nil {
		return nil, fmt.Errorf("invalid project result: %w (model reply: %s)", err, truncate(response))
	}
	return &result, nil
}

// truncate keeps error messages readable while still showing what the model
// actually replied — a validation failure is undebuggable otherwise.
func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// Retrieval attempts before scoring without a rubric, and the placeholder used
// when it never arrives. A transient retrieval failure used to score the
// candidate with no criteria at all, silently.
const (
	retrievalAttempts = 3
	fallbackContext   = "No additional context available."
)

// retrieveWithRetry fetches the rubric context, retrying transient failures. It
// reports whether the caller is now running degraded (no rubric).
func retrieveWithRetry(fetch func() (string, error), label string) (string, bool) {
	var lastErr error
	for attempt := 0; attempt < retrievalAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		text, err := fetch()
		switch {
		case err != nil:
			lastErr = err
		case strings.TrimSpace(text) == "":
			lastErr = fmt.Errorf("empty %s context", label)
		default:
			return text, false
		}
	}
	log.Printf("Warning: failed to retrieve %s context after %d attempts: %v", label, retrievalAttempts, lastErr)
	return fallbackContext, true
}

// generateSummary creates final overall summary
func (es *EvaluationService) generateSummary(ctx context.Context, cvResult *CVEvaluationResult, projectResult *ProjectEvaluationResult) (*FinalSummaryResult, error) {
	prompt := buildFinalSummaryPrompt(cvResult, projectResult)

	response, err := es.llmClient.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	var result FinalSummaryResult
	if err := llm.ParseJSONResponse(response, &result); err != nil {
		return nil, fmt.Errorf("failed to parse summary response: %w", err)
	}

	return &result, nil
}

// handleJobError updates job with error information
func (es *EvaluationService) handleJobError(jobID, errorMsg string, retryCount int) {
	log.Printf("[Job %s] Error: %s", jobID, errorMsg)
	if err := es.jobRepo.UpdateError(jobID, errorMsg, retryCount); err != nil {
		log.Printf("[Job %s] Failed to update error status: %v", jobID, err)
	}
}

// readPDFContent extracts text from PDF file
func readPDFContent(filePath string) (string, error) {
	file, reader, err := pdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer file.Close()

	var content string
	totalPages := reader.NumPage()

	for pageIndex := 1; pageIndex <= totalPages; pageIndex++ {
		page := reader.Page(pageIndex)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			log.Printf("Warning: failed to extract text from page %d: %v", pageIndex, err)
			continue
		}

		content += text + "\n\n"
	}

	if len(content) == 0 {
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	return content, nil
}

func validateCVResult(result *CVEvaluationResult) error {
	if result.CVMatchRate < 0 || result.CVMatchRate > 1 {
		return fmt.Errorf("cv_match_rate out of range: %f", result.CVMatchRate)
	}
	if len(result.CVFeedback) < 50 {
		return fmt.Errorf("cv_feedback too short")
	}
	return nil
}

func validateProjectResult(result *ProjectEvaluationResult) error {
	if result.ProjectScore < 1 || result.ProjectScore > 5 {
		return fmt.Errorf("project_score out of range: %f", result.ProjectScore)
	}
	if len(result.ProjectFeedback) < 50 {
		return fmt.Errorf("project_feedback too short")
	}
	return nil
}
