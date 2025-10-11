package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gaisuke/profx/internal/services"
	"github.com/go-playground/validator/v10"
)

type ResultHandler struct {
	jobService *services.JobService
	validator  *validator.Validate
}

func NewResultHandler(jobService *services.JobService, validator *validator.Validate) *ResultHandler {
	return &ResultHandler{
		jobService: jobService,
		validator:  validator,
	}
}

func (rh *ResultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/result/")
	jobID := strings.TrimSpace(path)

	if jobID == "" {
		sendJSONError(w, "Job ID is required", http.StatusBadRequest)
		return
	}

	job, err := rh.jobService.GetJobByID(jobID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			sendJSONError(w, "Job not found", http.StatusNotFound)
		} else {
			sendJSONError(w, "Failed to retrieve job: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	response := job.ToResponse()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
