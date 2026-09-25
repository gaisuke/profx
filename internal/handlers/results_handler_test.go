package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaisuke/profx/internal/models"
)

type fakeLister struct {
	jobs      []models.EvaluationJob
	err       error
	lastLimit int
}

func (f *fakeLister) ListRecent(limit int) ([]models.EvaluationJob, error) {
	f.lastLimit = limit
	return f.jobs, f.err
}

func TestResultsHandlerReturnsHistory(t *testing.T) {
	lister := &fakeLister{jobs: []models.EvaluationJob{{ID: "job-1", JobTitle: "Backend Engineer"}}}
	rec := httptest.NewRecorder()
	NewResultsHandler(lister).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not a JSON array: %v", err)
	}
	if len(got) != 1 || got[0]["id"] != "job-1" || got[0]["job_title"] != "Backend Engineer" {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if lister.lastLimit != defaultHistoryLimit {
		t.Errorf("default limit = %d, want %d", lister.lastLimit, defaultHistoryLimit)
	}
}

func TestResultsHandlerClampsAndRejectsLimit(t *testing.T) {
	lister := &fakeLister{}
	h := NewResultsHandler(lister)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/results?limit=9999", nil))
	if lister.lastLimit != maxHistoryLimit {
		t.Errorf("limit should clamp to %d, got %d", maxHistoryLimit, lister.lastLimit)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results?limit=abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a non-numeric limit must be rejected, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/results", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST must be rejected, got %d", rec.Code)
	}
}

func TestResultsHandlerReportsStoreFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	NewResultsHandler(&fakeLister{err: errors.New("db down")}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
