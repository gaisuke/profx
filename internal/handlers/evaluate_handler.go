package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/services"
	"github.com/go-playground/validator/v10"
)

type EvaluateHandler struct {
	jobService *services.JobService
	validator  *validator.Validate
}

func NewEvaluateHandler(jobService *services.JobService, validator *validator.Validate) *EvaluateHandler {
	return &EvaluateHandler{
		jobService: jobService,
		validator:  validator,
	}
}

type EvaluateRequest struct {
	JobTitle          string `json:"job_title" validate:"required"`
	CandidateCVID     string `json:"candidate_cv_id" validate:"required"`
	ProjectReportIDID string `json:"project_report_id" validate:"required"`
}

type EvaluateResponse struct {
	ID     string           `json:"id"`
	Status models.JobStatus `json:"status"`
}

func (eh *EvaluateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req EvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := eh.validator.Struct(&req); err != nil {
		sendJSONError(w, "Validation error: "+err.Error(), http.StatusBadRequest)
		return
	}

	job, err := eh.jobService.CreateJob(req.JobTitle, req.CandidateCVID, req.ProjectReportIDID)
	if err != nil {
		sendJSONError(w, "Failed to create evaluation job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	response := EvaluateResponse{
		ID:     job.ID,
		Status: job.Status,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(response)
}
