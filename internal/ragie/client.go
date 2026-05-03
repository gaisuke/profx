package ragie

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	ragieBaseURL   = "https://api.ragie.ai"
	defaultTimeout = 10 * time.Second
)

type Client struct {
	apiKey     string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

type RetrievalRequest struct {
	Query   string            `json:"query"`
	TopK    int               `json:"top_k"`
	Filters map[string]string `json:"filters,omitempty"`
}

type RetrievalResponse struct {
	Chunks []Chunk `json:"chunks"`
}

type Chunk struct {
	Text     string                 `json:"text"`
	Score    float64                `json:"score"`
	Metadata map[string]interface{} `json:"metadata"`
}

func (c *Client) Retrieve(query string, topK int, filters map[string]string) ([]Chunk, error) {
	reqBody := RetrievalRequest{
		Query:   query,
		TopK:    topK,
		Filters: filters,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", ragieBaseURL+"/retrieve", bytes.NewBuffer(jsonData))
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var retrievalResp RetrievalResponse
	if err := json.NewDecoder(resp.Body).Decode(&retrievalResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return retrievalResp.Chunks, nil
}

func (c *Client) RetrieveForCV(jobTitle string) (string, error) {
	query := fmt.Sprintf("%s requirements, technical skills, experience expectations", jobTitle)

	filters := map[string]string{
		"type": "job_desc, cv_rubric",
	}

	chunks, err := c.Retrieve(query, 5, filters)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve CV context: %w", err)
	}

	return chunksToContext(chunks), nil
}

func (c *Client) RetrieveForProject() (string, error) {
	query := "case study requirements, evaluation criteria, scoring rubric"

	filters := map[string]string{
		"type": "case_brief,project_rubric",
	}

	chunks, err := c.Retrieve(query, 5, filters)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve project context: %w", err)
	}

	return chunksToContext(chunks), nil
}

func chunksToContext(chunks []Chunk) string {
	var context string
	for i, chunk := range chunks {
		context += fmt.Sprintf("--- Chunk %d (relevance: %.2f) ---\n%s\n\n", i+1, chunk.Score, chunk.Text)
	}
	return context
}
