package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gaisuke/profx/internal/services"
)

// health answers "is this deployment wired up, and what is left of today's
// allowance". The retrieval block is not decoration: a corpus filter that
// matches nothing makes every evaluation run without a rubric, and the only
// visible symptom is feedback that reads a little softer.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := healthResponse{
		Status:   "ok",
		Provider: s.deps.Provider,
		Sources:  s.sourceNames(),
		Retrieval: retrievalDTO{
			Enabled: s.deps.Retriever != nil,
		},
	}
	if s.deps.Retriever != nil {
		resp.Retrieval.BaseURL = s.deps.Retriever.BaseURL()
		key, cvValues, projectValues := s.deps.Retriever.FilterConfigInfo()
		resp.Retrieval.FilterKey = key
		resp.Retrieval.CVFilterValues = cvValues
		resp.Retrieval.ProjectValues = projectValues
	}
	if s.deps.Checks != nil {
		if q, err := s.deps.Checks.Quota(ctx, clientIP(r)); err == nil {
			resp.Quotas.Checks = newCheckLimits(q)
		}
	}
	if s.deps.Searches != nil {
		if q, err := s.deps.Searches.Quota(ctx, clientIP(r)); err == nil {
			resp.Quotas.Searches = newSearchLimits(q)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// openAPISpec serves the specification the docs page loads, so the document and
// the running service can never drift apart by deployment: the file ships inside
// the binary.
func (s *server) openAPISpec(w http.ResponseWriter, r *http.Request) {
	if len(s.deps.OpenAPI) == 0 {
		writeError(w, http.StatusNotFound, codeNotFound, "spesifikasi OpenAPI tidak tersedia di build ini")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(s.deps.OpenAPI); err != nil {
		// The client went away; nothing to report to it.
		return
	}
}

// demoStatus renders the recruiter-side allowance for the health payload.
func demoStatus(d DemoGate, ctx context.Context) (services.DemoStatus, error) {
	if d == nil {
		return services.DemoStatus{}, errors.New("demo mode not configured")
	}
	return d.Status(ctx)
}
