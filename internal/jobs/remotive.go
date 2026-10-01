package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	list, err := parseRemotive(body)
	if err != nil {
		return nil, err
	}
	// Remotive states who may apply, so unlike the other remote boards we can
	// respect it. A posting restricted to "USA" or "Americas, Europe" is dropped
	// here rather than judged and rejected later: spending a model call to learn
	// that a Jakarta candidate cannot take a US-only job is waste.
	out := make([]Job, 0, len(list))
	for _, j := range list {
		if OpenToIndonesia(j.Location) {
			out = append(out, j)
		}
	}
	return out, nil
}

// openHints are the phrases RemoteOK-style boards use when a role is not
// geographically restricted. "Worldwide", regional groupings that include
// South-East Asia, and Indonesia itself.
var openHints = []string{
	"worldwide", "anywhere", "global", "asia", "apac", "asean", "sea ",
	"indonesia", "southeast asia", "south-east asia", "remote",
}

// OpenToIndonesia reports whether a posting's stated applicant location admits a
// candidate in Indonesia. An empty value is treated as open: several boards leave
// it blank when a role is unrestricted, and dropping those would lose real jobs.
// Phrases naming a region we are not in (USA, Europe, LATAM, Eastern Time) are
// rejected.
func OpenToIndonesia(location string) bool {
	loc := strings.ToLower(strings.TrimSpace(location))
	if loc == "" {
		return true
	}
	// A restriction we can recognise as foreign outweighs a generic word: a
	// "Remote - US" posting contains both "remote" and "us". Country codes get
	// their own pass because they appear as bare tokens ("Remote - US", "Berlin,
	// DE") that no phrase list catches.
	if hasClosedCountryCode(loc) {
		return false
	}
	for _, closed := range closedHints {
		if strings.Contains(loc, closed) {
			return false
		}
	}
	for _, hint := range openHints {
		if strings.Contains(loc, hint) {
			return true
		}
	}
	return false
}

// closedCodes are two-letter country or region codes that mean "not Indonesia".
// Codes that double as common English words are deliberately absent — "in", "my",
// "id", "no", "it" — because "anywhere in the world" must not read as India.
var closedCodes = map[string]bool{
	"us": true, "usa": true, "uk": true, "eu": true, "emea": true, "latam": true,
	"ca": true, "au": true, "de": true, "fr": true, "nl": true, "pl": true,
	"il": true, "jp": true, "kr": true, "cn": true, "ph": true, "sg": true,
	"nz": true, "br": true, "mx": true, "za": true, "ie": true, "at": true,
	"ch": true, "se": true, "dk": true, "no": true, "fi": true, "pt": true,
	"gr": true, "tr": true, "hk": true, "tw": true,
}

func hasClosedCountryCode(loc string) bool {
	for _, tok := range countryCodeRE.FindAllString(loc, -1) {
		if closedCodes[tok] {
			return true
		}
	}
	return false
}

// countryCodeRE splits a location into word-ish tokens so codes can be matched
// exactly rather than as substrings ("us" must not match "australia").
var countryCodeRE = regexp.MustCompile(`[a-z]{2,3}`)

var closedHints = []string{
	"usa", "u.s.", "united states", "america", "canada", "latam", "brazil",
	"mexico", "europe", "emea", "united kingdom", "germany", "france", "spain",
	"netherlands", "poland", "uk", " ireland", "israel", "africa", "australia",
	"new zealand", "japan", "korea", "china", "india", "philippines", "vietnam",
	"timezone", "time zone", "est", "pst", "cst", "cet",
	"malaysia", "singapore", "thailand", "brazil", "argentina", "colombia",
	"peru", "chile", "nigeria", "kenya", "egypt", "turkey", "pakistan",
	"bangladesh", "sri lanka", "nepal", "cambodia", "myanmar",
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
