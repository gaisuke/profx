package models

import "time"

// CekGap is one concrete problem found between the CV and the job description,
// with what to do about it. The public result shows three of these.
type CekGap struct {
	Bagian    string `json:"bagian"`
	Masalah   string `json:"masalah"`
	Perbaikan string `json:"perbaikan"`
}

// ContohPerbaikan is one rewritten line, so the visitor can see the difference
// between vague and specific before paying for the rest.
type ContohPerbaikan struct {
	Sebelum string `json:"sebelum"`
	Sesudah string `json:"sesudah"`
	Alasan  string `json:"alasan"`
}

// CekResult is the model's answer, before anything is seen by the visitor.
type CekResult struct {
	Skor      int             `json:"skor"`
	Ringkasan string          `json:"ringkasan"`
	Celah     []CekGap        `json:"celah"`
	Contoh    ContohPerbaikan `json:"contoh_perbaikan"`
}

// PublicCheck is a stored free check. It holds the score and the findings —
// never the CV text or the job description those findings came from.
type PublicCheck struct {
	ID        string    `json:"id"`
	JobTitle  string    `json:"job_title"`
	Score     int       `json:"skor"`
	Payload   CekResult `json:"hasil"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CekQuota is what the page shows about the free allowance. A limit nobody can
// see is indistinguishable from a broken app.
type CekQuota struct {
	PerIPLimit  int    `json:"per_ip_limit"`
	PerIPUsed   int    `json:"per_ip_used"`
	Remaining   int    `json:"remaining"`
	GlobalLimit int    `json:"global_limit"`
	GlobalUsed  int    `json:"global_used"`
	ResetsAt    string `json:"resets_at"`
	PaidOpen    bool   `json:"paid_open"`
}
