package api

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/ragie"
	"github.com/gaisuke/profx/internal/services"
)

// DemoGate is the recruiter-side allowance. It exists because every evaluation
// costs model calls, so a publicly reachable deployment needs a visible cap.
type DemoGate interface {
	Enabled() bool
	Allow(ctx context.Context) (remaining int, err error)
	Status(ctx context.Context) (services.DemoStatus, error)
}

// RetrieverDiagnostics is the view of the rubric corpus the health endpoint
// needs. A filter mismatch is otherwise invisible: retrieval answers 200 with
// nothing, the pipeline scores without a rubric, and the only symptom is softer
// feedback. Reporting the corpus's real metadata values turns that into a
// one-request diagnosis.
type RetrieverDiagnostics interface {
	RetrieveForCV(jobTitle string) (string, error)
	Documents() ([]ragie.Document, error)
	FilterConfigInfo() (key string, cvValues, projectValues []string)
	BaseURL() string
}

// Turnstile is the optional human check in front of the open endpoints.
type Turnstile interface {
	Verify(ctx context.Context, ip, token string) error
}

// DocumentUploader stores the two PDFs an evaluation needs.
type DocumentUploader interface {
	UploadDocuments(cvFile, reportFile io.Reader, cvFilename, reportFilename string) (*models.UploadResponse, error)
}

// JobStore is the recruiter-side evaluation store.
type JobStore interface {
	CreateJob(jobTitle, cvDocID, reportDocID string) (*models.EvaluationJob, error)
	GetJobByID(id string) (*models.EvaluationJob, error)
	ListRange(limit, offset int) ([]models.EvaluationJob, error)
}

// CheckService is the public CV check.
type CheckService interface {
	Evaluate(ctx context.Context, ip, jobTitle, jobDesc, cvText string) (*models.PublicCheck, error)
	Result(ctx context.Context, id string) (*models.PublicCheck, error)
	Quota(ctx context.Context, ip string) (models.CekQuota, error)
	Interest(ctx context.Context, checkID, contact, note string) error
}

// SearchService is the public job search.
type SearchService interface {
	Search(ctx context.Context, ip, cvText, location string, remoteOnly bool, maxResults int) (*models.CariRun, error)
	Result(ctx context.Context, id string) (*models.CariRun, error)
	Quota(ctx context.Context, ip string) (models.CariQuota, error)
}

// Deps is everything the REST surface needs. Explicit struct instead of a
// package-level global: a test can build one with fakes and exercise the real
// routing.
type Deps struct {
	Documents DocumentUploader
	Jobs      JobStore
	Checks    CheckService
	Searches  SearchService
	Demo      DemoGate
	Retriever RetrieverDiagnostics
	Turnstile Turnstile

	Provider string
	Sources  []string
	Origins  []string
	MaxBody  int64
	OpenAPI  []byte
}

type server struct {
	deps Deps
}

// NewRouter declares the whole REST surface. Every route lives here, so the
// OpenAPI document and the code can be compared line by line.
func NewRouter(deps Deps) http.Handler {
	s := &server{deps: deps}
	mux := http.NewServeMux()

	// Recruiter pipeline: documents in, evaluation out.
	mux.HandleFunc("POST /v1/documents", s.uploadDocuments)
	mux.HandleFunc("POST /v1/evaluations", s.createEvaluation)
	mux.HandleFunc("GET /v1/evaluations", s.listEvaluations)
	mux.HandleFunc("GET /v1/evaluations/{id}", s.getEvaluation)

	// Public CV check: one job description against one CV.
	mux.HandleFunc("POST /v1/checks", s.createCheck)
	mux.HandleFunc("GET /v1/checks/limits", s.checkLimits)
	mux.HandleFunc("GET /v1/checks/{id}", s.getCheck)
	mux.HandleFunc("POST /v1/checks/{id}/interest", s.registerInterest)

	// Public job search: a CV against public postings.
	mux.HandleFunc("POST /v1/searches", s.createSearch)
	mux.HandleFunc("GET /v1/searches/limits", s.searchLimits)
	mux.HandleFunc("GET /v1/searches/{id}", s.getSearch)

	// Ops and documentation.
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/openapi.yaml", s.openAPISpec)

	// The mux answers unknown paths and wrong methods in plain text; a client
	// written against this contract would choke on exactly those two responses.
	return withMiddleware(jsonNotFound{mux: mux}, deps.Origins, deps.MaxBody)
}

func (s *server) sourceNames() []string {
	if len(s.deps.Sources) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(s.deps.Sources))
	for _, n := range s.deps.Sources {
		out = append(out, strings.TrimSpace(n))
	}
	return out
}
