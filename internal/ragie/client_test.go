package ragie

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A deployment without a RAGIE_API_KEY must degrade, not die: the retriever
// reports "not configured" and the pipeline marks the evaluation as running
// without rubric context.
func TestNoopClientReportsNotConfigured(t *testing.T) {
	var c NoopClient
	if _, err := c.RetrieveForCV("Backend Engineer"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("RetrieveForCV: got %v, want ErrNotConfigured", err)
	}
	if _, err := c.RetrieveForProject(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("RetrieveForProject: got %v, want ErrNotConfigured", err)
	}
}

// stubRetrieval runs a server that records the request it received and answers
// with the given body.
func stubRetrieval(t *testing.T, status int, response string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	var gotReq http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = *r
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotReq, &gotBody
}

const scoredChunksBody = `{
  "scored_chunks": [
    {"id":"c1","text":"RUBRIC: backend roles need Go and Postgres.","score":0.91,
     "document_id":"d1","document_name":"cv_rubric_backend.md","document_metadata":{"type":"cv_rubric"}},
    {"id":"c2","text":"JOB DESC: own the ingestion pipeline end to end.","score":0.77,
     "document_id":"d2","document_name":"jd_backend.md","document_metadata":{"type":"job_desc"}}
  ]
}`

// The client must post to the documented endpoint with the documented payload.
// The previous version called /retrieve (404) and sent "filters" instead of
// "filter", so no request ever reached a working retrieval.
func TestRetrieveUsesDocumentedEndpointAndPayload(t *testing.T) {
	srv, gotReq, gotBody := stubRetrieval(t, http.StatusOK, scoredChunksBody)

	c := NewClient("test-key").WithBaseURL(srv.URL)
	if _, err := c.RetrieveForCV("Backend Engineer"); err != nil {
		t.Fatalf("RetrieveForCV: %v", err)
	}

	if gotReq.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", gotReq.Method)
	}
	if gotReq.URL.Path != "/retrievals" {
		t.Errorf("path = %s, want /retrievals", gotReq.URL.Path)
	}
	if got := gotReq.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", got)
	}

	var payload map[string]any
	if err := json.Unmarshal(*gotBody, &payload); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, string(*gotBody))
	}
	if _, ok := payload["filters"]; ok {
		t.Error("request body still contains the undocumented plural key \"filters\"")
	}
	if q, _ := payload["query"].(string); !strings.Contains(q, "Backend Engineer") {
		t.Errorf("query = %q, want it to mention the job title", q)
	}
	if topK, ok := payload["top_k"].(float64); !ok || int(topK) != 5 {
		t.Errorf("top_k = %v, want 5", payload["top_k"])
	}

	// The filter must be an operator object, not a comma-joined string.
	filter, ok := payload["filter"].(map[string]any)
	if !ok {
		t.Fatalf("filter = %#v, want an object", payload["filter"])
	}
	typeFilter, ok := filter["type"].(map[string]any)
	if !ok {
		t.Fatalf("filter.type = %#v, want an operator object", filter["type"])
	}
	in, ok := typeFilter["$in"].([]any)
	if !ok {
		t.Fatalf("filter.type.$in = %#v, want an array", typeFilter["$in"])
	}
	got := make([]string, 0, len(in))
	for _, v := range in {
		got = append(got, v.(string))
	}
	if strings.Join(got, ",") != "job_desc,cv_rubric" {
		t.Errorf("filter.type.$in = %v, want [job_desc cv_rubric]", got)
	}
}

// The two retrieval calls must ask for different document sets, otherwise the
// project stage would score against the CV rubric.
func TestRetrieveForProjectUsesProjectFilterValues(t *testing.T) {
	srv, _, gotBody := stubRetrieval(t, http.StatusOK, scoredChunksBody)

	c := NewClient("test-key").WithBaseURL(srv.URL)
	if _, err := c.RetrieveForProject(); err != nil {
		t.Fatalf("RetrieveForProject: %v", err)
	}

	var payload struct {
		Filter map[string]struct {
			In []string `json:"$in"`
		} `json:"filter"`
	}
	if err := json.Unmarshal(*gotBody, &payload); err != nil {
		t.Fatalf("bad request body: %v", err)
	}
	got := strings.Join(payload.Filter["type"].In, ",")
	if got != "case_brief,project_rubric" {
		t.Errorf("project filter = %q, want case_brief,project_rubric", got)
	}
}

// Real chunks must reach the prompt, with their source document named.
func TestRetrieveDecodesScoredChunksIntoContext(t *testing.T) {
	srv, _, _ := stubRetrieval(t, http.StatusOK, scoredChunksBody)

	c := NewClient("test-key").WithBaseURL(srv.URL)
	context, err := c.RetrieveForCV("Backend Engineer")
	if err != nil {
		t.Fatalf("RetrieveForCV: %v", err)
	}
	if !strings.Contains(context, "backend roles need Go and Postgres") {
		t.Errorf("context is missing the first chunk:\n%s", context)
	}
	if !strings.Contains(context, "own the ingestion pipeline") {
		t.Errorf("context is missing the second chunk:\n%s", context)
	}
	if !strings.Contains(context, `cv_rubric_backend.md`) {
		t.Errorf("context does not name the source document:\n%s", context)
	}
}

// Regression: an empty result must be an error, not a silently empty context.
// The old client decoded a "chunks" key that does not exist in the response, so
// a perfect 200 gave the model nothing and nobody noticed.
func TestRetrieveEmptyResultIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"documented shape, no match": `{"scored_chunks":[]}`,
		"old assumed shape":          `{"chunks":[{"text":"ignored","score":0.9}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _, _ := stubRetrieval(t, http.StatusOK, body)
			c := NewClient("test-key").WithBaseURL(srv.URL)
			if _, err := c.RetrieveForCV("Backend Engineer"); !errors.Is(err, ErrNoChunks) {
				t.Fatalf("got %v, want ErrNoChunks", err)
			}
		})
	}
}

// A rejected request must say why: Ragie returns {"detail": ...}.
func TestRetrieveSurfacesAPIErrorDetail(t *testing.T) {
	srv, _, _ := stubRetrieval(t, http.StatusUnauthorized, `{"detail":"invalid api key","status_code":401}`)

	c := NewClient("bad-key").WithBaseURL(srv.URL)
	_, err := c.RetrieveForCV("Backend Engineer")
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	if !strings.Contains(err.Error(), "invalid api key") || !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to carry the status and detail", err)
	}
}

// The corpus owns the metadata strings, so they must be overridable.
func TestFilterConfigFromEnv(t *testing.T) {
	t.Setenv("RAGIE_FILTER_KEY", "doc_type")
	t.Setenv("RAGIE_CV_FILTER", "jd, rubric")
	t.Setenv("RAGIE_PROJECT_FILTER", "brief")

	cfg := DefaultFilterConfig()
	if cfg.Key != "doc_type" {
		t.Errorf("Key = %q, want doc_type", cfg.Key)
	}
	if strings.Join(cfg.CVValues, "|") != "jd|rubric" {
		t.Errorf("CVValues = %v, want [jd rubric]", cfg.CVValues)
	}
	if strings.Join(cfg.ProjectValues, "|") != "brief" {
		t.Errorf("ProjectValues = %v, want [brief]", cfg.ProjectValues)
	}

	srv, _, gotBody := stubRetrieval(t, http.StatusOK, scoredChunksBody)
	c := NewClientWithFilters("test-key", cfg).WithBaseURL(srv.URL)
	if _, err := c.RetrieveForCV("Backend Engineer"); err != nil {
		t.Fatalf("RetrieveForCV: %v", err)
	}
	if !strings.Contains(string(*gotBody), `"doc_type"`) {
		t.Errorf("request body did not use the configured filter key: %s", string(*gotBody))
	}
}
