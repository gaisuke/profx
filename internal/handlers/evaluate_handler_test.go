package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gaisuke/profx/internal/services"
	"github.com/go-playground/validator/v10"
)

type stubGate struct {
	enabled   bool
	remaining int
	err       error
	calls     int
}

func (s *stubGate) Enabled() bool { return s.enabled }

func (s *stubGate) Allow(context.Context) (int, error) { s.calls++; return s.remaining, s.err }

// A public demo must refuse with 429 once the day's budget is gone, and say why.
// The gate runs before any job is created, so a nil job service is enough to
// prove the refusal happens first.
func TestEvaluateRefusesWhenDemoQuotaIsExhausted(t *testing.T) {
	gate := &stubGate{enabled: true, err: services.ErrDemoQuotaExceeded}
	h := NewEvaluateHandler(nil, validator.New(), gate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluate",
		strings.NewReader(`{"job_title":"Backend Engineer","candidate_cv_id":"cv","project_report_id":"rep"}`))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (%s)", rec.Code, rec.Body.String())
	}
	if gate.calls != 1 {
		t.Errorf("quota checked %d times, want 1", gate.calls)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("a rate limit must tell the caller when to come back")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "today's evaluations") {
		t.Errorf("error = %q, want it to explain the daily cap", msg)
	}
}

// A failing quota check is our problem, not the visitor's: it must be a 500, not
// a silent allowance to keep spending.
func TestEvaluateFailsClosedWhenQuotaCheckBreaks(t *testing.T) {
	gate := &stubGate{enabled: true, err: errors.New("db down")}
	h := NewEvaluateHandler(nil, validator.New(), gate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluate",
		strings.NewReader(`{"job_title":"Backend Engineer","candidate_cv_id":"cv","project_report_id":"rep"}`))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// Validation still comes first: a malformed request must not consume quota.
func TestEvaluateValidatesBeforeTouchingQuota(t *testing.T) {
	gate := &stubGate{enabled: true, remaining: 5}
	h := NewEvaluateHandler(nil, validator.New(), gate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluate", strings.NewReader(`{"job_title":""}`))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if gate.calls != 0 {
		t.Errorf("quota was checked %d times for an invalid request, want 0", gate.calls)
	}
}

func TestEvaluateWithoutGateBehavesAsBefore(t *testing.T) {
	h := NewEvaluateHandler(nil, validator.New(), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/evaluate", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// The public demo must not list other people's evaluations.
func TestResultsHistoryIsDisabledInDemoMode(t *testing.T) {
	h := NewResultsHandler(&fakeLister{}, &stubGate{enabled: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results?limit=5", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "history is disabled") {
		t.Errorf("body = %s, want an explanation", rec.Body.String())
	}
}
