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
		Company     struct {
			Code string `json:"code"`
		} `json:"company"`
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
	// The board holds over a thousand live Indonesian postings, so there is room
	// to search wide: the CV's own strongest keywords, plus one unfiltered pass to
	// catch local postings whose wording none of the keywords match. Each keyword
	// is paged once, because the ordering is by recency and the second page is
	// where the older-but-still-open local roles live.
	const (
		limit        = 50
		maxKeywords  = 6
		pagesPerText = 2
	)
	texts := []string{""}
	seen := map[string]bool{}
	for _, kw := range q.Keywords {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw == "" || seen[kw] || len(texts) > maxKeywords {
			continue
		}
		seen[kw] = true
		texts = append(texts, kw)
	}

	var out []Job
	for _, text := range texts {
		for page := 0; page < pagesPerText; page++ {
			url := fmt.Sprintf("https://www.kalibrr.com/kjs/job_board/search?limit=%d&offset=%d&country=Indonesia",
				limit, page*limit)
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
			if len(batch) < limit {
				break // that keyword has no more pages
			}
		}
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

// kalibrrURL builds the posting's public address.
//
// The company code is not decoration: /jobs/<id>/<slug> renders "This is not the
// web page you are looking for" — a 200 with a shell, which is why curl alone
// never caught it — while /c/<code>/jobs/<id>/<slug> renders the posting. Without
// the code the link is dead on arrival, so it falls back to the company listing
// rather than handing out an address that looks right and 404s in the browser.
func kalibrrURL(id int, slug, companyCode string) string {
	slug = strings.TrimSpace(slug)
	companyCode = strings.TrimSpace(companyCode)
	if companyCode == "" {
		// Rare (50 of 50 sampled postings carried a code). A search for the title
		// still lands the visitor on the posting; inventing a company-less address
		// would land them on "page not available".
		return "https://www.kalibrr.com/job-board?text=" + urlQueryEscape(slug)
	}
	if slug == "" {
		return fmt.Sprintf("https://www.kalibrr.com/c/%s/jobs/%d", companyCode, id)
	}
	return fmt.Sprintf("https://www.kalibrr.com/c/%s/jobs/%d/%s", companyCode, id, slug)
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
			URL:         kalibrrURL(j.ID, j.Slug, j.Company.Code),
			Description: truncate(desc, 6000),
			Tags:        tags,
			PublishedAt: parseTime(j.Activation),
		})
	}
	return out, nil
}
