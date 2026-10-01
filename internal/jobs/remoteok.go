package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RemoteOK returns its whole board as a single JSON array whose first element is
// a legal notice rather than a posting — a detail worth keeping in the parser,
// because a naive one turns that notice into a job.
type RemoteOK struct{}

func NewRemoteOK() *RemoteOK { return &RemoteOK{} }

func (r *RemoteOK) Name() string { return "remoteok" }

func (r *RemoteOK) Fetch(ctx context.Context, q Query) ([]Job, error) {
	body, err := getJSON(ctx, "https://remoteok.com/api")
	if err != nil {
		return nil, err
	}
	return parseRemoteOK(body)
}

// remoteokItem is decoded leniently: the legal notice entry has none of the job
// fields, and salary arrives as an int in some entries and a string in others.
type remoteokItem struct {
	Slug        string          `json:"slug"`
	ID          string          `json:"id"`
	Date        string          `json:"date"`
	Company     string          `json:"company"`
	Position    string          `json:"position"`
	Tags        []string        `json:"tags"`
	Description string          `json:"description"`
	Location    string          `json:"location"`
	URL         string          `json:"url"`
	ApplyURL    string          `json:"apply_url"`
	SalaryMin   json.RawMessage `json:"salary_min"`
	Legal       string          `json:"legal"`
}

func parseRemoteOK(body []byte) ([]Job, error) {
	var items []remoteokItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("remoteok: jawaban tidak bisa dibaca: %w", err)
	}
	out := make([]Job, 0, len(items))
	for _, it := range items {
		// The first element is the API's terms-of-service notice.
		if strings.TrimSpace(it.Legal) != "" {
			continue
		}
		if strings.TrimSpace(it.Position) == "" || it.ID == "" {
			continue
		}
		tags := make([]string, 0, len(it.Tags))
		for _, t := range it.Tags {
			if s := strings.TrimSpace(strings.ToLower(t)); s != "" {
				tags = append(tags, s)
			}
		}
		url := it.URL
		if url == "" {
			url = it.ApplyURL
		}
		out = append(out, Job{
			Source:      "remoteok",
			ExternalID:  it.ID,
			Title:       strings.TrimSpace(it.Position),
			Company:     strings.TrimSpace(it.Company),
			Location:    strings.TrimSpace(it.Location),
			Remote:      true,
			URL:         url,
			Description: truncate(stripHTML(it.Description), 6000),
			Tags:        tags,
			PublishedAt: parseTime(it.Date),
		})
	}
	return out, nil
}
