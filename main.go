package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	uploadDir  = "./uploads"
	maxFileSize = 10 << 20 // 10 MB
)

type UploadResponse struct {
	CandidateCVID    string `json:"candidate_cv_id"`
	ProjectReportID  string `json:"project_report_id"`
	Message          string `json:"message"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func main() {
	// Ensure upload directory exists
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatal("Failed to create upload directory:", err)
	}

	http.HandleFunc("/upload", handleUpload)
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s...\n", port)
	log.Printf("Upload endpoint: POST http://localhost:%s/upload\n", port)
	
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form with max memory
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

	// Validate file types (PDF only)
	if !isPDF(cvHeader.Filename) {
		sendJSONError(w, "candidate_cv must be a PDF file", http.StatusBadRequest)
		return
	}
	if !isPDF(reportHeader.Filename) {
		sendJSONError(w, "project_report must be a PDF file", http.StatusBadRequest)
		return
	}

	// Generate unique IDs for files
	cvID := uuid.New().String()
	reportID := uuid.New().String()

	// Save candidate CV
	cvPath := filepath.Join(uploadDir, cvID+".pdf")
	if err := saveFile(cvFile, cvPath); err != nil {
		sendJSONError(w, "Failed to save candidate_cv: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Save project report
	reportPath := filepath.Join(uploadDir, reportID+".pdf")
	if err := saveFile(reportFile, reportPath); err != nil {
		// Clean up CV file if report fails
		os.Remove(cvPath)
		sendJSONError(w, "Failed to save project_report: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Log successful upload
	log.Printf("Files uploaded successfully - CV: %s, Report: %s\n", cvID, reportID)

	// Return success response with IDs
	response := UploadResponse{
		CandidateCVID:   cvID,
		ProjectReportID: reportID,
		Message:         "Files uploaded successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

func saveFile(src io.Reader, dstPath string) error {
	// Create destination file
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	// Copy file content
	_, err = io.Copy(dst, src)
	return err
}

func isPDF(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".pdf" || ext == ".PDF"
}

func sendJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}
