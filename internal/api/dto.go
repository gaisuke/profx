package api

import (
	"time"

	"github.com/gaisuke/profx/internal/models"
)

// The API owns its own response types rather than reusing the database models.
// Two reasons: the contract promises explicit nulls for unfinished numbers
// (omitempty in the storage layer silently drops the keys instead), and the
// public fields of a stored row are not a public interface.

type documentsResponse struct {
	CandidateCVID   string `json:"candidate_cv_id"`
	ProjectReportID string `json:"project_report_id"`
}

type evaluationResponse struct {
	ID              string     `json:"id"`
	JobTitle        string     `json:"job_title"`
	Status          string     `json:"status"`
	CVMatchRate     *float64   `json:"cv_match_rate"`
	CVFeedback      *string    `json:"cv_feedback"`
	ProjectScore    *float64   `json:"project_score"`
	ProjectFeedback *string    `json:"project_feedback"`
	OverallSummary  *string    `json:"overall_summary"`
	ErrorMessage    *string    `json:"error_message"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at"`
}

func newEvaluationResponse(job *models.EvaluationJob) evaluationResponse {
	resp := evaluationResponse{
		ID:        job.ID,
		JobTitle:  job.JobTitle,
		Status:    string(job.Status),
		CreatedAt: job.CreatedAt.UTC(),
	}
	if job.CVMatchRate.Valid {
		v := job.CVMatchRate.Float64
		resp.CVMatchRate = &v
	}
	if job.CVFeedback.Valid {
		v := job.CVFeedback.String
		resp.CVFeedback = &v
	}
	if job.ProjectScore.Valid {
		v := job.ProjectScore.Float64
		resp.ProjectScore = &v
	}
	if job.ProjectFeedback.Valid {
		v := job.ProjectFeedback.String
		resp.ProjectFeedback = &v
	}
	if job.OverallSummary.Valid {
		v := job.OverallSummary.String
		resp.OverallSummary = &v
	}
	if job.ErrorMessage.Valid {
		v := job.ErrorMessage.String
		resp.ErrorMessage = &v
	}
	if job.CompletedAt.Valid {
		v := job.CompletedAt.Time.UTC()
		resp.CompletedAt = &v
	}
	return resp
}

type links struct {
	Self string `json:"self"`
	Page string `json:"page,omitempty"`
}

// --- public CV check -------------------------------------------------------

type gapDTO struct {
	Section string `json:"section"`
	Problem string `json:"problem"`
	Fix     string `json:"fix"`
}

type sampleFixDTO struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Reason string `json:"reason"`
}

type checkResultDTO struct {
	Score     int          `json:"score"`
	Summary   string       `json:"summary"`
	Gaps      []gapDTO     `json:"gaps"`
	SampleFix sampleFixDTO `json:"sample_fix"`
}

type checkResponse struct {
	ID        string         `json:"id"`
	JobTitle  string         `json:"job_title"`
	Score     int            `json:"score"`
	Result    checkResultDTO `json:"result"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	Links     links          `json:"links"`
}

// newCheckResponse translates the stored verdict, whose Go fields are named in
// Indonesian because that is what the model was asked to produce, into the
// English field names the API contract promises.
func newCheckResponse(c *models.PublicCheck) checkResponse {
	gaps := make([]gapDTO, 0, len(c.Payload.Celah))
	for _, g := range c.Payload.Celah {
		gaps = append(gaps, gapDTO{Section: g.Bagian, Problem: g.Masalah, Fix: g.Perbaikan})
	}
	return checkResponse{
		ID:       c.ID,
		JobTitle: c.JobTitle,
		Score:    c.Score,
		Result: checkResultDTO{
			Score:   c.Payload.Skor,
			Summary: c.Payload.Ringkasan,
			Gaps:    gaps,
			SampleFix: sampleFixDTO{
				Before: c.Payload.Contoh.Sebelum,
				After:  c.Payload.Contoh.Sesudah,
				Reason: c.Payload.Contoh.Alasan,
			},
		},
		CreatedAt: c.CreatedAt.UTC(),
		ExpiresAt: c.ExpiresAt.UTC(),
		Links: links{
			Self: "/profx/api/v1/checks/" + c.ID,
			Page: "/profx/cek/hasil.html?id=" + c.ID,
		},
	}
}

type limitsResponse struct {
	PerIPPerDay  int       `json:"per_ip_per_day"`
	Used         int       `json:"used"`
	Remaining    int       `json:"remaining"`
	GlobalPerDay int       `json:"global_per_day"`
	GlobalUsed   int       `json:"global_used"`
	ResetsAt     time.Time `json:"resets_at"`
}

func newCheckLimits(q models.CekQuota) limitsResponse {
	return limitsResponse{
		PerIPPerDay:  q.PerIPLimit,
		Used:         q.PerIPUsed,
		Remaining:    q.Remaining,
		GlobalPerDay: q.GlobalLimit,
		GlobalUsed:   q.GlobalUsed,
		ResetsAt:     parseOrNow(q.ResetsAt),
	}
}

func newSearchLimits(q models.CariQuota) limitsResponse {
	return limitsResponse{
		PerIPPerDay:  q.PerIPLimit,
		Used:         q.PerIPUsed,
		Remaining:    q.Remaining,
		GlobalPerDay: q.GlobalLimit,
		GlobalUsed:   q.GlobalUsed,
		ResetsAt:     parseOrNow(q.ResetsAt),
	}
}

func parseOrNow(rfc3339 string) time.Time {
	if t, err := time.Parse(time.RFC3339, rfc3339); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}

// --- job search ------------------------------------------------------------

type jobDTO struct {
	Title       string     `json:"title"`
	Company     string     `json:"company"`
	Location    string     `json:"location"`
	Remote      bool       `json:"remote"`
	Source      string     `json:"source"`
	URL         string     `json:"url"`
	PublishedAt *time.Time `json:"published_at"`
}

type searchResultDTO struct {
	Score  int      `json:"score"`
	Reason string   `json:"reason"`
	Gaps   []string `json:"gaps"`
	Job    jobDTO   `json:"job"`
}

type progressDTO struct {
	Scored int `json:"scored"`
	Total  int `json:"total"`
}

type searchResponse struct {
	ID        string            `json:"id"`
	Status    string            `json:"status"`
	Message   string            `json:"message"`
	Progress  progressDTO       `json:"progress"`
	Results   []searchResultDTO `json:"results"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
	Links     links             `json:"links"`
}

// searchStatus maps the run's internal state onto the contract's vocabulary.
func searchStatus(s string) string {
	switch s {
	case "selesai":
		return "done"
	case "gagal":
		return "failed"
	default:
		return "running"
	}
}

func newSearchResponse(run *models.CariRun) searchResponse {
	results := make([]searchResultDTO, 0, len(run.Hasil))
	for _, item := range run.Hasil {
		gaps := item.Celah
		if gaps == nil {
			gaps = []string{}
		}
		var published *time.Time
		if !item.Job.PublishedAt.IsZero() {
			p := item.Job.PublishedAt.UTC()
			published = &p
		}
		results = append(results, searchResultDTO{
			Score:  item.Skor,
			Reason: item.Alasan,
			Gaps:   gaps,
			Job: jobDTO{
				Title:       item.Job.Title,
				Company:     item.Job.Company,
				Location:    item.Job.Location,
				Remote:      item.Job.Remote,
				Source:      item.Job.Source,
				URL:         item.Job.URL,
				PublishedAt: published,
			},
		})
	}
	return searchResponse{
		ID:        run.ID,
		Status:    searchStatus(run.Status),
		Message:   run.Pesan,
		Progress:  progressDTO{Scored: run.Selesai, Total: run.Total},
		Results:   results,
		CreatedAt: run.CreatedAt.UTC(),
		ExpiresAt: run.ExpiresAt.UTC(),
		Links:     links{Self: "/profx/api/v1/searches/" + run.ID, Page: "/profx/cari/index.html?id=" + run.ID},
	}
}

// --- ops -------------------------------------------------------------------

type retrievalDTO struct {
	Enabled        bool     `json:"enabled"`
	BaseURL        string   `json:"base_url,omitempty"`
	FilterKey      string   `json:"filter_key,omitempty"`
	CVFilterValues []string `json:"cv_filter_values,omitempty"`
	ProjectValues  []string `json:"project_filter_values,omitempty"`
}

type quotasDTO struct {
	Checks   limitsResponse `json:"checks"`
	Searches limitsResponse `json:"searches"`
}

type healthResponse struct {
	Status    string       `json:"status"`
	Provider  string       `json:"provider"`
	Retrieval retrievalDTO `json:"retrieval"`
	Sources   []string     `json:"sources"`
	Quotas    quotasDTO    `json:"quotas"`
}
