package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Kalibrr is the local one: a South-East Asia board (Indonesia and the
// Philippines), which is why it is worth having next to the remote-heavy
// western boards. Its public job board API needs no key; limit and offset are
// both required.
type Kalibrr struct{}

func NewKalibrr() *Kalibrr { return &Kalibrr{} }

func (k *Kalibrr) Name() string { return "kalibrr" }

type kalibrrResponse struct {
	Count int `json:"count"`
	Jobs  []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		CompanyName string `json:"company_name"`
		Activation  string `json:"activation_date"`
		WorkFromAny bool   `json:"is_work_from_home"`
		Hybrid      bool   `json:"is_hybrid"`
		Function    string `json:"function"`
		Tenure      string `json:"tenure"`
		Qualif      string `json:"qualifications"`
		Location    *struct {
			AddressComponents struct {
				City    string `json:"city"`
				Region  string `json:"region"`
				Country string `json:"country"`
			} `json:"address_components"`
		} `json:"google_location"`
	} `json:"jobs"`
}

// Fetch queries Kalibrr for Indonesia only. Its default listing is SEA-wide and
// comes back Philippines-heavy, which is noise for a candidate in Jakarta: the
// whole point of including this board is local postings the western boards do
// not carry.
//
// The board's own search accepts a keyword, so the CV's strongest keywords drive
// it, plus one unfiltered pass to catch local postings whose wording none of the
// keywords match.
func (k *Kalibrr) Fetch(ctx context.Context, q Query) ([]Job, error) {
	const limit = 50
	texts := []string{""}
	seen := map[string]bool{}
	for _, kw := range q.Keywords {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw == "" || seen[kw] || len(texts) > 3 {
			continue
		}
		seen[kw] = true
		texts = append(texts, kw)
	}

	var out []Job
	for _, text := range texts {
		url := fmt.Sprintf("https://www.kalibrr.com/kjs/job_board/search?limit=%d&offset=0&country=Indonesia", limit)
		if text != "" {
			url += "&text=" + urlQueryEscape(text)
		}
		body, err := getJSON(ctx, url)
		if err != nil {
			if len(out) > 0 {
				break // a failing keyword must not fail the whole search
			}
			return nil, err
		}
		batch, err := parseKalibrr(body)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

// urlQueryEscape keeps the query string safe without importing net/url in a file
// that otherwise builds URLs by hand.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteString("%20")
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}

// parseKalibrr is separate from the HTTP call so the parser can be tested
// against a canned payload with no network.
func parseKalibrr(body []byte) ([]Job, error) {
	var resp kalibrrResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("kalibrr: jawaban tidak bisa dibaca: %w", err)
	}
	out := make([]Job, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		if j.ID == 0 || j.Name == "" {
			continue
		}
		loc := ""
		remote := j.WorkFromAny
		if j.Location != nil {
			parts := []string{}
			for _, p := range []string{j.Location.AddressComponents.City, j.Location.AddressComponents.Region, j.Location.AddressComponents.Country} {
				if strings.TrimSpace(p) != "" {
					parts = append(parts, strings.TrimSpace(p))
				}
			}
			loc = strings.Join(parts, ", ")
			// The board is SEA-wide; only the country tells us where it is.
			if strings.EqualFold(j.Location.AddressComponents.Country, "Indonesia") {
				loc = strings.TrimSuffix(loc, ", Philippines")
			}
		}
		desc := stripHTML(j.Description)
		if strings.TrimSpace(j.Qualif) != "" {
			desc += "\n\nKualifikasi:\n" + stripHTML(j.Qualif)
		}
		tags := []string{}
		if j.Function != "" {
			tags = append(tags, strings.ToLower(j.Function))
		}
		if j.Tenure != "" {
			tags = append(tags, strings.ToLower(j.Tenure))
		}
		out = append(out, Job{
			Source:      "kalibrr",
			ExternalID:  fmt.Sprintf("%d", j.ID),
			Title:       strings.TrimSpace(j.Name),
			Company:     strings.TrimSpace(j.CompanyName),
			Location:    loc,
			Remote:      remote,
			URL:         fmt.Sprintf("https://www.kalibrr.com/jobs/%d/%s", j.ID, j.Slug),
			Description: truncate(desc, 6000),
			Tags:        tags,
			PublishedAt: parseTime(j.Activation),
		})
	}
	return out, nil
}
