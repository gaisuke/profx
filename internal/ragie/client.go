package ragie

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// ragieBaseURL is the default provider. RAGIE_BASE_URL points the client at
	// any service that speaks the same two endpoints, which is how the pipeline
	// switched to the self-hosted corpus without touching the pipeline itself.
	ragieBaseURL = "https://api.ragie.ai"

	// retrievePath is the documented endpoint. Ragie's retrieval API lives at
	// /retrievals (plural) and takes a singular "filter" object; the previous
	// version posted to /retrieve with a plural "filters" map and a 404'd on
	// every call, which made the whole pipeline run without a rubric.
	retrievePath = "/retrievals"

	defaultTimeout = 30 * time.Second
)

// ErrNotConfigured is returned by NoopClient when no RAGIE_API_KEY is set.
var ErrNotConfigured = errors.New("ragie: no API key configured")

// ErrNoChunks means retrieval answered 200 but matched no chunks at all. That is
// almost always a filter mismatch (metadata key or value that no document
// carries), so it is reported separately from a transport failure.
var ErrNoChunks = errors.New("ragie: retrieval matched no chunks")

// FilterConfig describes how documents in the Ragie corpus are tagged, so the
// two retrieval calls can ask for the documents they actually need. Values are
// overridable by env because the corpus, not the code, owns these strings.
type FilterConfig struct {
	Key           string
	CVValues      []string
	ProjectValues []string
}

// DefaultFilterConfig reads the corpus layout from the environment.
func DefaultFilterConfig() FilterConfig {
	return FilterConfig{
		Key:           envOr("RAGIE_FILTER_KEY", "type"),
		CVValues:      splitList(envOr("RAGIE_CV_FILTER", "job_desc,cv_rubric")),
		ProjectValues: splitList(envOr("RAGIE_PROJECT_FILTER", "case_brief,project_rubric")),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type Client struct {
	apiKey     string
	filter     FilterConfig
	httpClient *http.Client
	baseURL    string
}

func NewClient(apiKey string) *Client {
	return NewClientWithFilters(apiKey, DefaultFilterConfig())
}

func NewClientWithFilters(apiKey string, cfg FilterConfig) *Client {
	if cfg.Key == "" {
		cfg.Key = "type"
	}
	return &Client{
		apiKey:     apiKey,
		filter:     cfg,
		baseURL:    ragieBaseURL,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

// WithBaseURL points the client at another host. Tests use it to assert the
// exact request shape against a stub server; production uses it to read from a
// self-hosted corpus instead of the hosted provider.
func (c *Client) WithBaseURL(baseURL string) *Client {
	if baseURL != "" {
		c.baseURL = strings.TrimSuffix(baseURL, "/")
	}
	return c
}

// BaseURL reports where this client is pointed, for diagnostics.
func (c *Client) BaseURL() string { return c.baseURL }

type RetrievalRequest struct {
	Query  string         `json:"query"`
	TopK   int            `json:"top_k"`
	Filter map[string]any `json:"filter,omitempty"`
}

// RetrievalResponse mirrors the documented response: chunks arrive under
// "scored_chunks". Decoding a "chunks" key yielded an empty slice with no error,
// so retrieval looked successful while handing the model no rubric at all.
type RetrievalResponse struct {
	ScoredChunks []ScoredChunk `json:"scored_chunks"`
}

type ScoredChunk struct {
	ID           string         `json:"id"`
	Text         string         `json:"text"`
	Score        float64        `json:"score"`
	DocumentID   string         `json:"document_id"`
	DocumentName string         `json:"document_name"`
	Metadata     map[string]any `json:"document_metadata"`
}

// Chunk is the subset of a scored chunk the evaluation pipeline consumes.
type Chunk struct {
	Text     string
	Score    float64
	Source   string
	Metadata map[string]any
}

type apiError struct {
	Detail     string `json:"detail"`
	StatusCode int    `json:"status_code"`
}

// inFilter builds the metadata filter for one retrieval: match any of the
// given values. Ragie takes an operator object ("$in"), not a comma-joined
// string — "job_desc, cv_rubric" never matched a single document.
func (c *Client) inFilter(values []string) map[string]any {
	return map[string]any{
		c.filter.Key: map[string]any{"$in": values},
	}
}

func (c *Client) Retrieve(query string, topK int, filter map[string]any) ([]Chunk, error) {
	reqBody := RetrievalRequest{Query: query, TopK: topK, Filter: filter}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+retrievePath, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var e apiError
		if json.Unmarshal(body, &e) == nil && e.Detail != "" {
			return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, e.Detail)
		}
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var retrievalResp RetrievalResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&retrievalResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	chunks := make([]Chunk, 0, len(retrievalResp.ScoredChunks))
	for _, sc := range retrievalResp.ScoredChunks {
		if strings.TrimSpace(sc.Text) == "" {
			continue
		}
		chunks = append(chunks, Chunk{Text: sc.Text, Score: sc.Score, Source: sc.DocumentName, Metadata: sc.Metadata})
	}
	return chunks, nil
}

func (c *Client) RetrieveForCV(jobTitle string) (string, error) {
	query := fmt.Sprintf("%s requirements, technical skills, experience expectations", jobTitle)
	return c.retrieveContext(query, c.inFilter(c.filter.CVValues))
}

func (c *Client) RetrieveForProject() (string, error) {
	query := "case study requirements, evaluation criteria, scoring rubric"
	return c.retrieveContext(query, c.inFilter(c.filter.ProjectValues))
}

func (c *Client) retrieveContext(query string, filter map[string]any) (string, error) {
	chunks, err := c.Retrieve(query, 5, filter)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		return "", fmt.Errorf("%w (filter %s=%v)", ErrNoChunks, c.filter.Key, filter[c.filter.Key])
	}
	return chunksToContext(chunks), nil
}

func chunksToContext(chunks []Chunk) string {
	var b strings.Builder
	for i, chunk := range chunks {
		name := chunk.Source
		if name == "" {
			name, _ = chunk.Metadata["document_name"].(string)
		}
		if name == "" {
			name, _ = chunk.Metadata["title"].(string)
		}
		source := ""
		if name != "" {
			source = fmt.Sprintf(" from %q", name)
		}
		fmt.Fprintf(&b, "--- Chunk %d (relevance: %.2f)%s ---\n%s\n\n", i+1, chunk.Score, source, chunk.Text)
	}
	return b.String()
}

// Document is the part of a corpus document the ops endpoint reports.
type Document struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Status   string         `json:"status"`
	Metadata map[string]any `json:"metadata"`
}

type documentsResponse struct {
	Documents []Document `json:"documents"`
}

// Documents lists the corpus. Paired with FilterConfigInfo it answers the
// question that cost a full deploy cycle: "is the filter key/value I send the
// one my documents actually carry?"
func (c *Client) Documents() ([]Document, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/documents", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var e apiError
		if json.Unmarshal(body, &e) == nil && e.Detail != "" {
			return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, e.Detail)
		}
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var out documentsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return out.Documents, nil
}

// FilterConfigInfo exposes the configured corpus layout for diagnostics.
func (c *Client) FilterConfigInfo() (key string, cvValues, projectValues []string) {
	return c.filter.Key, c.filter.CVValues, c.filter.ProjectValues
}

// NoopClient answers every retrieval with ErrNotConfigured. It lets the service
// start and keep evaluating when retrieval is not configured: the pipeline marks
// those results as running without rubric context instead of refusing to boot.
type NoopClient struct{}

func (NoopClient) RetrieveForCV(string) (string, error) { return "", ErrNotConfigured }

func (NoopClient) RetrieveForProject() (string, error) { return "", ErrNotConfigured }

// Documents keeps the ops endpoint working on a deployment without a key.
func (NoopClient) Documents() ([]Document, error) { return nil, ErrNotConfigured }

// FilterConfigInfo reports the defaults so diagnostics stay uniform.
func (NoopClient) FilterConfigInfo() (string, []string, []string) {
	cfg := DefaultFilterConfig()
	return cfg.Key, cfg.CVValues, cfg.ProjectValues
}

// BaseURL reports that no host is configured.
func (NoopClient) BaseURL() string { return "" }
