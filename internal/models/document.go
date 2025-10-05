package models

// UploadRequest represents the expected upload request data
type UploadRequest struct {
	CandidateCV   []byte
	ProjectReport []byte
}

// UploadResponse represents the response after successful upload
type UploadResponse struct {
	CandidateCVID   string `json:"candidate_cv_id"`
	ProjectReportID string `json:"project_report_id"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// Document represents a stored document
type Document struct {
	ID       string
	Filename string
	Path     string
}
