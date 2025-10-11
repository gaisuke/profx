package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/services"
)

const maxFileSize = 10 << 20 // 10 MB

// UploadHandler handles document upload requests
type UploadHandler struct {
	service *services.DocumentService
}

// NewUploadHandler creates a new UploadHandler
func NewUploadHandler(service *services.DocumentService) *UploadHandler {
	return &UploadHandler{
		service: service,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *UploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(maxFileSize); err != nil {
		sendJSONError(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Get candidate CV file
	cvFile, cvHeader, err := r.FormFile("candidate_cv")
	if err != nil {
		sendJSONError(w, "candidate_cv is required", http.StatusBadRequest)
		return
	}
	defer cvFile.Close()

	// Get project report file
	reportFile, reportHeader, err := r.FormFile("project_report")
	if err != nil {
		sendJSONError(w, "project_report is required", http.StatusBadRequest)
		return
	}
	defer reportFile.Close()

	// Upload documents via service
	response, err := h.service.UploadDocuments(
		cvFile, reportFile,
		cvHeader.Filename, reportHeader.Filename,
	)
	if err != nil {
		if err == services.ErrInvalidFileType {
			sendJSONError(w, "Both files must be PDF format", http.StatusBadRequest)
		} else {
			sendJSONError(w, "Failed to upload files: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Log successful upload
	log.Printf("Files uploaded successfully - CV: %s, Report: %s\n",
		response.CandidateCVID, response.ProjectReportID)

	// Send success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

func sendJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(models.ErrorResponse{Error: message})
}
