package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaisuke/profx/internal/ragie"
)

type stubRetriever struct {
	context     string
	retrieveErr error
	docs        []ragie.Document
	docsErr     error
}

func (s stubRetriever) RetrieveForCV(string) (string, error) { return s.context, s.retrieveErr }

func (s stubRetriever) Documents() ([]ragie.Document, error) { return s.docs, s.docsErr }

func (s stubRetriever) FilterConfigInfo() (string, []string, []string) {
	return "type", []string{"job_desc", "cv_rubric"}, []string{"case_brief", "project_rubric"}
}

func get(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: response is not JSON: %v (%s)", path, err, rec.Body.String())
	}
	return rec.Code, body
}

func TestHealthzReportsIntegrations(t *testing.T) {
	h := NewOpsHandler(stubRetriever{docs: []ragie.Document{{ID: "d1"}}}, "opencodego")
	code, body := get(t, h, "/healthz")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["provider"] != "opencodego" {
		t.Errorf("provider = %v, want opencodego", body["provider"])
	}
	ragieInfo := body["ragie"].(map[string]any)
	if ragieInfo["configured"] != true {
		t.Errorf("ragie.configured = %v, want true", ragieInfo["configured"])
	}
}

// An unreachable corpus must show up as an explicit reason, not as "ok".
func TestHealthzSurfacesRagieFailure(t *testing.T) {
	h := NewOpsHandler(stubRetriever{docsErr: errors.New("request failed: TLS handshake timeout")}, "opencodego")
	_, body := get(t, h, "/healthz")
	ragieInfo := body["ragie"].(map[string]any)
	if ragieInfo["configured"] != false {
		t.Errorf("ragie.configured = %v, want false", ragieInfo["configured"])
	}
	if detail, _ := ragieInfo["detail"].(string); detail == "" {
		t.Error("ragie.detail must carry the failure reason")
	}
}

// The check must answer the only question that matters when a retrieval comes
// back empty: do my documents actually carry the metadata I filter on?
func TestRetrievalCheckReportsCorpusMetadataAndMatches(t *testing.T) {
	docs := []ragie.Document{
		{ID: "d1", Name: "rubric.md", Metadata: map[string]any{"type": "cv_rubric"}},
		{ID: "d2", Name: "jd.md", Metadata: map[string]any{"type": "job_desc"}},
		{ID: "d3", Name: "brief.md", Metadata: map[string]any{"type": "case_brief"}},
		{ID: "d4", Name: "other.md", Metadata: map[string]any{"kind": "note"}},
	}
	h := NewOpsHandler(stubRetriever{context: "--- Chunk 1 (relevance: 0.90) ---\nrubric text\n", docs: docs}, "opencodego")

	code, body := get(t, h, "/retrieval-check")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	retrieval := body["cv_retrieval"].(map[string]any)
	if retrieval["ok"] != true || retrieval["chunks"].(float64) != 1 {
		t.Errorf("cv_retrieval = %v, want ok with 1 chunk", retrieval)
	}
	corpus := body["corpus"].(map[string]any)
	if corpus["documents"].(float64) != 4 {
		t.Errorf("corpus.documents = %v, want 4", corpus["documents"])
	}
	values := corpus["metadata_values"].(map[string]any)["type"].(map[string]any)
	if values["cv_rubric"].(float64) != 1 || values["job_desc"].(float64) != 1 {
		t.Errorf("metadata_values[type] = %v, want the real corpus tags", values)
	}
	match := body["filter_match"].(map[string]any)
	if match["documents_matching_cv_filter"].(float64) != 2 {
		t.Errorf("documents_matching_cv_filter = %v, want 2", match["documents_matching_cv_filter"])
	}
	if match["documents_matching_project_filter"].(float64) != 1 {
		t.Errorf("documents_matching_project_filter = %v, want 1", match["documents_matching_project_filter"])
	}
}

// A filter that matches nothing (the deployed bug) must be visible, not silent.
func TestRetrievalCheckFlagsFilterThatMatchesNothing(t *testing.T) {
	docs := []ragie.Document{{ID: "d1", Metadata: map[string]any{"type": "resume_template"}}}
	h := NewOpsHandler(stubRetriever{
		retrieveErr: errors.New("ragie: retrieval matched no chunks"),
		docs:        docs,
	}, "opencodego")

	_, body := get(t, h, "/retrieval-check")
	retrieval := body["cv_retrieval"].(map[string]any)
	if retrieval["ok"] != false {
		t.Error("cv_retrieval.ok must be false when retrieval failed")
	}
	if got, _ := retrieval["error"].(string); got == "" {
		t.Error("cv_retrieval.error must carry the reason")
	}
	match := body["filter_match"].(map[string]any)
	if match["documents_matching_cv_filter"].(float64) != 0 {
		t.Error("a filter matching no document must be reported as 0 matches")
	}
}
