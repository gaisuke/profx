package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gaisuke/profx/internal/ragie"
)

// RetrieverDiagnostics is the view of the retriever the ops endpoints need.
type RetrieverDiagnostics interface {
	RetrieveForCV(jobTitle string) (string, error)
	Documents() ([]ragie.Document, error)
	FilterConfigInfo() (key string, cvValues, projectValues []string)
}

// OpsHandler serves the deployment's self-checks:
//
//	GET /healthz          liveness plus which integrations are wired
//	GET /retrieval-check  one live retrieval against Ragie, with the corpus
//	                      metadata that a filter has to match
//
// The second endpoint exists because a filter mismatch is invisible: retrieval
// answers 200 with an empty result and the pipeline then scores without a rubric.
// Reporting the corpus's real metadata values turns that silent failure into a
// one-request diagnosis.
type OpsHandler struct {
	retriever RetrieverDiagnostics
	provider  string
}

func NewOpsHandler(retriever RetrieverDiagnostics, provider string) *OpsHandler {
	return &OpsHandler{retriever: retriever, provider: provider}
}

func (h *OpsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		h.health(w)
	case "/retrieval-check":
		h.retrievalCheck(w)
	default:
		sendJSONError(w, "Not found", http.StatusNotFound)
	}
}

func (h *OpsHandler) health(w http.ResponseWriter) {
	key, _, _ := h.retriever.FilterConfigInfo()
	_, docsErr := h.retriever.Documents()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"provider": h.provider,
		"ragie": map[string]any{
			"configured": docsErr == nil,
			"filter_key": key,
			"detail":     errString(docsErr),
		},
	})
}

func (h *OpsHandler) retrievalCheck(w http.ResponseWriter) {
	key, cvValues, projectValues := h.retriever.FilterConfigInfo()

	start := time.Now()
	context, cvErr := h.retriever.RetrieveForCV("Backend Engineer")
	cvMS := time.Since(start).Milliseconds()

	corpus := map[string]any{"documents": 0, "error": nil}
	matchesCV, matchesProject := 0, 0

	docs, err := h.retriever.Documents()
	if err != nil {
		corpus["error"] = err.Error()
	} else {
		corpus["documents"] = len(docs)
		corpus["metadata_values"] = metadataValueCounts(docs)
		matchesCV = countMatches(docs, key, cvValues)
		matchesProject = countMatches(docs, key, projectValues)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"filter_key":     key,
		"cv_filter":      cvValues,
		"project_filter": projectValues,
		"cv_retrieval": map[string]any{
			"ok":      cvErr == nil,
			"chunks":  chunkCount(context),
			"took_ms": cvMS,
			"error":   errString(cvErr),
		},
		"corpus": corpus,
		"filter_match": map[string]any{
			"documents_matching_cv_filter":      matchesCV,
			"documents_matching_project_filter": matchesProject,
		},
	})
}

// metadataValueCounts lists, per metadata key, the values present in the corpus.
func metadataValueCounts(docs []ragie.Document) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, d := range docs {
		for k, v := range d.Metadata {
			if out[k] == nil {
				out[k] = map[string]int{}
			}
			out[k][fmt.Sprintf("%v", v)]++
		}
	}
	return out
}

// countMatches counts documents carrying one of the wanted values under key.
func countMatches(docs []ragie.Document, key string, wanted []string) int {
	set := map[string]bool{}
	for _, w := range wanted {
		set[w] = true
	}
	n := 0
	for _, d := range docs {
		v, ok := d.Metadata[key]
		if !ok {
			continue
		}
		switch typed := v.(type) {
		case string:
			if set[typed] {
				n++
			}
		case []any:
			for _, item := range typed {
				if set[fmt.Sprintf("%v", item)] {
					n++
					break
				}
			}
		}
	}
	return n
}

func chunkCount(context string) int {
	n := 0
	for i := 0; i+8 < len(context); i++ {
		if context[i:i+8] == "--- Chun" {
			n++
		}
	}
	return n
}

func errString(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// sortedKeys is used by the diagnostics test to compare metadata maps.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
