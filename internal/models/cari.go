package models

import "time"

// MatchItem is one judged posting, ordered best first on the result page.
type MatchItem struct {
	Skor   int        `json:"skor"`
	Alasan string     `json:"alasan"`
	Celah  []string   `json:"celah"`
	Job    JobSummary `json:"lowongan"`
}

// JobSummary is the posting as the visitor sees it. The full description stays
// on the server: it is long, it belongs to the board that published it, and the
// link goes there.
type JobSummary struct {
	Title       string    `json:"judul"`
	Company     string    `json:"perusahaan"`
	Location    string    `json:"lokasi"`
	Remote      bool      `json:"remote"`
	Source      string    `json:"sumber"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"terbit,omitempty"`
}

// JobRow is a posting as it is written to the database. It lives here so the
// storage layer and the matching service share one type instead of two that
// happen to look alike.
type JobRow struct {
	ID          string
	Source      string
	ExternalID  string
	Title       string
	Company     string
	Location    string
	Remote      bool
	URL         string
	Description string
	Tags        []string
	PublishedAt time.Time
}

// MatchJudgement is the model's answer for one posting.
type MatchJudgement struct {
	Skor   int      `json:"skor"`
	Alasan string   `json:"alasan"`
	Celah  []string `json:"celah"`
}

// CariRun is one search: its progress while it works, its results when done.
type CariRun struct {
	ID        string      `json:"id"`
	Status    string      `json:"status"`
	Pesan     string      `json:"pesan"`
	Total     int         `json:"total"`
	Selesai   int         `json:"selesai"`
	Hasil     []MatchItem `json:"hasil"`
	CreatedAt time.Time   `json:"dibuat"`
	ExpiresAt time.Time   `json:"kedaluwarsa"`
}

// CariQuota is the free allowance for searches, shown before the visitor spends
// one.
type CariQuota struct {
	PerIPLimit  int    `json:"per_ip_limit"`
	PerIPUsed   int    `json:"per_ip_used"`
	Remaining   int    `json:"remaining"`
	GlobalLimit int    `json:"global_limit"`
	GlobalUsed  int    `json:"global_used"`
	ResetsAt    string `json:"resets_at"`
}
