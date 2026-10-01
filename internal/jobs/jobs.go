// Package jobs fetches public job postings and decides, cheaply, which ones are
// worth spending a model call on.
//
// The split matters: fetching and keyword relevance are free and deterministic,
// so they can filter thousands of postings down to a dozen. Only those dozen go
// to the model. A ranking that calls the model for every posting would cost
// minutes and real money per run.
package jobs

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Job is one posting, normalised across sources.
type Job struct {
	Source      string    `json:"sumber"`
	ExternalID  string    `json:"external_id"`
	Title       string    `json:"judul"`
	Company     string    `json:"perusahaan"`
	Location    string    `json:"lokasi"`
	Remote      bool      `json:"remote"`
	URL         string    `json:"url"`
	Description string    `json:"-"` // plain text; not sent to the browser
	Tags        []string  `json:"tags,omitempty"`
	PublishedAt time.Time `json:"terbit,omitempty"`
}

// Key identifies a posting across runs and sources.
func (j Job) Key() string {
	return j.Source + ":" + j.ExternalID
}

// Query narrows a fetch. Sources that cannot honour a field ignore it rather
// than failing: a filter that silently does nothing is worse than none, so the
// service applies the real filtering itself.
type Query struct {
	Keywords []string
	Location string
	Limit    int
}

// Source is one job board.
type Source interface {
	Name() string
	Fetch(ctx context.Context, q Query) ([]Job, error)
}

// Sources are the boards reachable from this host without an API key or a proxy.
//
// Excluded on purpose: id.jobstreet.com and glints.com answer 403 to this host
// (Cloudflare), and scraping LinkedIn's HTML works but is both brittle and a
// terms-of-service problem we are not going to take on for a first version.
func Sources() []Source {
	return []Source{NewKalibrr(), NewRemotive(), NewRemoteOK(), NewArbeitnow()}
}

// DefaultSourceNames is the set this deployment runs with, and it is deliberately
// short: the product is for candidates in Indonesia.
//
//	kalibrr  — the local market, and the only board here that says which city a
//	           job is in. 1,057 live postings for Indonesia when this was measured.
//	remotive — kept for remote roles, but filtered to postings whose own
//	           candidate_required_location admits Indonesia, Asia or anywhere.
//
// remoteok and arbeitnow are implemented, tested and switchable, but off by
// default: remoteok's postings carry no eligibility signal (its location field is
// empty or a US city, and an empty field is not evidence a candidate in Jakarta
// can take the job), and arbeitnow's board is dominated by on-site German roles.
// Serving those to an Indonesian candidate is how the first live run ended up
// recommending Berlin on-site work.
func DefaultSourceNames() []string {
	return []string{"kalibrr", "remotive"}
}

// Select resolves source names to sources, ignoring names it does not know so a
// typo in configuration narrows the set instead of breaking the service. An
// empty result falls back to the defaults.
func Select(names []string) []Source {
	byName := map[string]Source{}
	for _, s := range Sources() {
		byName[s.Name()] = s
	}
	out := make([]Source, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(strings.ToLower(n))
		if s, ok := byName[n]; ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return Select(DefaultSourceNames())
	}
	return out
}

// client is shared: a browser User-Agent is not optional here, several of these
// boards sit behind Cloudflare and reject anything that looks like a script.
var client = &http.Client{Timeout: 45 * time.Second}

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/128.0 Safari/537.36"

func getJSON(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	body := make([]byte, 0, 1<<20)
	buf := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
		if len(body) > 12<<20 {
			break // a runaway response is not worth buffering
		}
	}
	return body, nil
}

var (
	tagRE    = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRE  = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankRE  = regexp.MustCompile(`\n{3,}`)
	entityRE = regexp.MustCompile(`&[a-zA-Z#0-9]{2,8};`)
)

// stripHTML turns a posting body into readable text. We do not need fidelity,
// we need the words: the model reads this, and so does the relevance filter.
func stripHTML(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p>", "\n")
	s = strings.ReplaceAll(s, "</li>", "\n")
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = entityRE.ReplaceAllString(s, " ")
	s = spaceRE.ReplaceAllString(s, " ")
	s = blankRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// truncate caps stored text so a talkative posting cannot bloat the database.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// sortByDateDesc puts the freshest posting first, so a Limit keeps the newest.
func sortByDateDesc(list []Job) {
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].PublishedAt.After(list[j].PublishedAt)
	})
}

// parseTime accepts the handful of layouts these boards actually emit.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05",
		"2006-01-02", time.RFC1123Z, time.RFC1123,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}
