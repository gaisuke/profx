package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gaisuke/profx/internal/models"
)

// JobLister is the read-only view of the job store this handler needs, declared
// here so the endpoint can be tested without a database.
type JobLister interface {
	ListRecent(limit int) ([]models.EvaluationJob, error)
}

// ResultsHandler serves the evaluation history: GET /results?limit=N
type ResultsHandler struct {
	jobs JobLister
}

func NewResultsHandler(jobs JobLister) *ResultsHandler {
	return &ResultsHandler{jobs: jobs}
}

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
)

func (rh *ResultsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := defaultHistoryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSONError(w, "limit must be a positive integer", http.StatusBadRequest)
			return
		}
		limit = parsed
		if limit > maxHistoryLimit {
			limit = maxHistoryLimit
		}
	}

	jobs, err := rh.jobs.ListRecent(limit)
	if err != nil {
		sendJSONError(w, "Failed to list evaluations: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Same response shape as GET /result/{id} so a client renders history and a
	// single result with one code path.
	history := make([]any, 0, len(jobs))
	for _, job := range jobs {
		history = append(history, job.ToResponse())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(history)
}
