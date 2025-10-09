package models

type DocumentType string

const (
	DocumentTypeCV            DocumentType = "cv"
	DocumentTypeProjectReport DocumentType = "project_report"
)

type UploadRequest struct {
	CandidateCV   []byte
	ProjectReport []byte
}

type UploadResponse struct {
	CandidateCVID   string `json:"candidate_cv_id"`
	ProjectReportID string `json:"project_report_id"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type Document struct {
	ID               string       `json:"id" db:"id"`
	Type             DocumentType `json:"type" db:"type"`
	OriginalFilename string       `json:"original_filename" db:"original_filename"`
	FilePath         string       `json:"file_path" db:"file_path"`
	UploadedAt       int64        `json:"uploaded_at" db:"uploaded_at"`
}
