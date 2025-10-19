package services

import (
	"context"
	"fmt"
	"log"

	"github.com/gaisuke/profx/internal/llm"
	"github.com/gaisuke/profx/internal/ragie"
	"github.com/gaisuke/profx/internal/storage"
	"github.com/gaisuke/profx/internal/utils"
)

type EvaluationService struct {
	jobRepo      *storage.JobRepository
	documentRepo *storage.DocumentRepository
	ragieClient  *ragie.Client
	llmClient    *llm.GeminiClient
}

func NewEvaluationService(
	jobRepo *storage.JobRepository,
	documentRepo *storage.DocumentRepository,
	ragieClient *ragie.Client,
	llmClient *llm.GeminiClient,
) *EvaluationService {
	return &EvaluationService{
		jobRepo:      jobRepo,
		documentRepo: documentRepo,
		ragieClient:  ragieClient,
		llmClient:    llmClient,
	}
}

type CVEvaluationResult struct {
	CVMatchRate float64 `json:"cv_match_rate"`
	CVFeedback  string  `json:"cv_feedback"`
}

type ProjectEvaluationResult struct {
	ProjectScore    float64 `json:"project_score"`
	ProjectFeedback string  `json:"project_feedback"`
}

type FinalSummaryResult struct {
	OverallSummary string `json:"overall_summary"`
}

func (es *EvaluationService) EvaluateJob(ctx context.Context, jobID string) error {
	log.Printf("[Job %s] Starting evaluation pipeline", jobID)

	job, err := es.jobRepo.GetByID(jobID)
	if err != nil {
		return fmt.Errorf("Failed to get job: %w", err)
	}

	if err := es.jobRepo.UpdateStatus(job); err != nil {
		log.Printf("[Job %s] Failed to update job status: %v", jobID, err)
	}

	cvDoc, err := es.documentRepo.GetDocumentByID(job.CVDocumentID)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("Failed to get CV document: %v", err), 0)
		return fmt.Errorf("Failed to get CV document: %w", err)
	}

	reportDoc, err := es.documentRepo.GetDocumentByID(job.ReportDocumentID)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("Failed to get report document: %v", err), 0)
		return fmt.Errorf("Failed to get report document: %w", err)
	}

	log.Printf("[Job %s] Stage 1: Evaluating CV", jobID)
	cvResult, err := es.evaluateCV(ctx, job.JobTitle, cvDoc.FilePath)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("CV evaluation failed: %v", err), 0)
		return fmt.Errorf("CV evaluation failed: %w", err)
	}

	log.Printf("[Job %s] Stage 1: Evaluating Project Report", jobID)
	projectResult, err := es.evaluateProject(ctx, job.JobTitle, reportDoc.FilePath)
	if err != nil {
		es.handleJobError(jobID, fmt.Sprintf("Project evaluation failed: %v", err), 0)
		return fmt.Errorf("Project evaluation failed: %w", err)
	}

	// Continue with the evaluation pipeline...
	return nil
}

func (es *EvaluationService) handleJobError(jobID, errorMessage string, retryCount int) {
	log.Printf("[Job %s] Error: %s", jobID, errorMessage)
}

func (es *EvaluationService) evaluateCV(ctx context.Context, jobTitle, cvFilePath string) (*CVEvaluationResult, error) {
	context, err := es.ragieClient.RetrieveForCV(jobTitle)
	if err != nil {
		log.Printf("Warning: Failed to retrieve CV context from Ragie: %v", err)
		context = "No additional context available."
	}

	cvContent, err := utils.ReadPDFContent(cvFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CV: %w", err)
	}

	prompt := buildCVEvaluationPrompt(context, cvContent, jobTitle)
	response, err := es.llmClient.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	var result CVEvaluationResult
	if err := parseJSONResponse(response, &result); err != nil {
		return nil, fmt.Errorf("failed to parse CV evaluation response: %w", err)
	}

	if err := validateCVResult(&result); err != nil {
		return nil, fmt.Errorf("invalid CV result: %w", err)
	}

	return &result, nil
}

func (es *EvaluationService) evaluateProject(ctx context.Context, jobTitle, reportFilePath string) (*ProjectEvaluationResult, error) {
	context, err := es.ragieClient.RetrieveForProject()
	if err != nil {
		log.Printf("Warning: Failed to retrieve project context from Ragie: %v", err)
		context = "No additional context available."
	}

	reportContent, err := utils.ReadPDFContent(reportFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project report: %w", err)
	}

	prompt := buildProjectEvaluationPrompt(context, reportContent)
	response, err := es.llmClient.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	var result ProjectEvaluationResult
	if err := parseJSONResponse(response, &result); err != nil {
		return nil, fmt.Errorf("failed to parse project evaluation response: %w", err)
	}

	if err := validateProjectResult(&result); err != nil {
		return nil, fmt.Errorf("invalid project result: %w", err)
	}

	return &result, nil
}

func (es *EvaluationService) generateSummary(ctx context.Context, cvResult *CVEvaluationResult, projectResult *ProjectEvaluationResult) (*FinalSummaryResult, error) {
	prompt := buildFinalSummaryPrompt(cvResult, projectResult)
	response, err := es.llmClient.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call for final summary failed: %w", err)
	}

	var result FinalSummaryResult
	if err:= parseJSONResponse(response, &cvResult); err != nil {
		return nil, fmt.Errorf()
	}
}