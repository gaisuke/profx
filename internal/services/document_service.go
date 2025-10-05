package services

import (
	"errors"
	"io"
	"path/filepath"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/storage"
)

var (
	ErrInvalidFileType = errors.New("file must be a PDF")
	ErrSaveFailed      = errors.New("failed to save file")
)

// DocumentService handles business logic for document operations
type DocumentService struct {
	storage storage.Storage
}

// NewDocumentService creates a new DocumentService
func NewDocumentService(storage storage.Storage) *DocumentService {
	return &DocumentService{
		storage: storage,
	}
}

// UploadDocuments handles the upload of CV and project report
func (ds *DocumentService) UploadDocuments(cvFile io.Reader, cvFilename string, reportFile io.Reader, reportFilename string) (*models.UploadResponse, error) {
	// Validate file types
	if !isPDF(cvFilename) {
		return nil, ErrInvalidFileType
	}
	if !isPDF(reportFilename) {
		return nil, ErrInvalidFileType
	}
	
	// Save candidate CV
	cvDoc, err := ds.storage.Save(cvFile, cvFilename)
	if err != nil {
		return nil, err
	}
	
	// Save project report
	reportDoc, err := ds.storage.Save(reportFile, reportFilename)
	if err != nil {
		// Note: In production, consider cleanup of cvDoc on failure
		return nil, err
	}
	
	return &models.UploadResponse{
		CandidateCVID:   cvDoc.ID,
		ProjectReportID: reportDoc.ID,
		Message:         "Files uploaded successfully",
	}, nil
}

func isPDF(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".pdf" || ext == ".PDF"
}
