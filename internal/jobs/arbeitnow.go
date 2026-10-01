package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Arbeitnow is the Europe-heavy board of the set. It answers with a big page,
// which is fine: the relevance filter is what keeps the model bill small.
type Arbeitnow struct{}

func NewArbeitnow() *Arbeitnow { return &Arbeitnow{} }

func (a *Arbeitnow) Name() string { return "arbeitnow" }

type arbeitnowResponse struct {
	Data []struct {
		Slug        string   `json:"slug"`
		CompanyName string   `json:"company_name"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Remote      bool     `json:"remote"`
		URL         string   `json:"url"`
		Tags        []string `json:"tags"`
		JobTypes    []string `json:"job_types"`
		Location    string   `json:"location"`
		CreatedAt   int64    `json:"created_at"`
	} `json:"data"`
}

func (a *Arbeitnow) Fetch(ctx context.Context, q Query) ([]Job, error) {
	body, err := getJSON(ctx, "https://www.arbeitnow.com/api/job-board-api")
	if err != nil {
		return nil, err
	}
	return parseArbeitnow(body)
}

func parseArbeitnow(body []byte) ([]Job, error) {
	var resp arbeitnowResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("arbeitnow: jawaban tidak bisa dibaca: %w", err)
	}
	out := make([]Job, 0, len(resp.Data))
	for _, j := range resp.Data {
		if strings.TrimSpace(j.Title) == "" {
			continue
		}
		tags := make([]string, 0, len(j.Tags)+len(j.JobTypes))
		for _, t := range append(append([]string{}, j.Tags...), j.JobTypes...) {
			if s := strings.TrimSpace(strings.ToLower(t)); s != "" {
				tags = append(tags, s)
			}
		}
		var published = timeFromUnix(j.CreatedAt)
		out = append(out, Job{
			Source:      "arbeitnow",
			ExternalID:  j.Slug,
			Title:       strings.TrimSpace(j.Title),
			Company:     strings.TrimSpace(j.CompanyName),
			Location:    strings.TrimSpace(j.Location),
			Remote:      j.Remote,
			URL:         j.URL,
			Description: truncate(stripHTML(j.Description), 6000),
			Tags:        tags,
			PublishedAt: published,
		})
	}
	return out, nil
}
