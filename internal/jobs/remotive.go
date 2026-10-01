package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Remotive publishes an open JSON feed of remote roles. It asks callers to keep
// the volume modest, which is why the service fetches a limited page and does
// its filtering locally.
type Remotive struct{}

func NewRemotive() *Remotive { return &Remotive{} }

func (r *Remotive) Name() string { return "remotive" }

type remotiveResponse struct {
	Jobs []struct {
		ID           int      `json:"id"`
		URL          string   `json:"url"`
		Title        string   `json:"title"`
		CompanyName  string   `json:"company_name"`
		Category     string   `json:"category"`
		Tags         []string `json:"tags"`
		JobType      string   `json:"job_type"`
		Published    string   `json:"publication_date"`
		Location     string   `json:"candidate_required_location"`
		Salary       string   `json:"salary"`
		Description  string   `json:"description"`
		CompanyLogo  string   `json:"company_logo"`
		CompanyLogos string   `json:"company_logo_url"`
	} `json:"jobs"`
}

func (r *Remotive) Fetch(ctx context.Context, q Query) ([]Job, error) {
	limit := 100
	if q.Limit > 0 && q.Limit < limit {
		limit = q.Limit
	}
	body, err := getJSON(ctx, fmt.Sprintf("https://remotive.com/api/remote-jobs?limit=%d", limit))
	if err != nil {
		return nil, err
	}
	return parseRemotive(body)
}

func parseRemotive(body []byte) ([]Job, error) {
	var resp remotiveResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("remotive: jawaban tidak bisa dibaca: %w", err)
	}
	out := make([]Job, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		if j.ID == 0 || strings.TrimSpace(j.Title) == "" {
			continue
		}
		tags := make([]string, 0, len(j.Tags)+2)
		for _, t := range j.Tags {
			if s := strings.TrimSpace(strings.ToLower(t)); s != "" {
				tags = append(tags, s)
			}
		}
		if j.Category != "" {
			tags = append(tags, strings.ToLower(j.Category))
		}
		out = append(out, Job{
			Source:      "remotive",
			ExternalID:  fmt.Sprintf("%d", j.ID),
			Title:       strings.TrimSpace(j.Title),
			Company:     strings.TrimSpace(j.CompanyName),
			Location:    strings.TrimSpace(j.Location),
			Remote:      true,
			URL:         j.URL,
			Description: truncate(stripHTML(j.Description), 6000),
			Tags:        tags,
			PublishedAt: parseTime(j.Published),
		})
	}
	return out, nil
}
