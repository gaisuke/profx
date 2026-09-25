package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/services"
	"github.com/go-playground/validator/v10"
)

// QuotaGate decides whether more work may be accepted right now. It exists so a
// publicly reachable deployment cannot be turned into an open wallet: every
// evaluation costs model calls, and the cap is visible rather than implied.
type QuotaGate interface {
	Enabled() bool
	Allow(ctx context.Context) (remaining int, err error)
}

type EvaluateHandler struct {
	jobService *services.JobService
	validator  *validator.Validate
	quota      QuotaGate
}

func NewEvaluateHandler(jobService *services.JobService, validator *validator.Validate, quota QuotaGate) *EvaluateHandler {
	return &EvaluateHandler{
		jobService: jobService,
		validator:  validator,
		quota:      quota,
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

	if eh.quota != nil && eh.quota.Enabled() {
		remaining, err := eh.quota.Allow(r.Context())
		if errors.Is(err, services.ErrDemoQuotaExceeded) {
			w.Header().Set("Retry-After", "3600")
			sendJSONError(w, "This public demo has used all of today's evaluations. "+err.Error(), http.StatusTooManyRequests)
			return
		}
		if err != nil {
			sendJSONError(w, "Could not check the demo quota: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("accepted evaluation; %d left in today's demo quota", remaining)
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
