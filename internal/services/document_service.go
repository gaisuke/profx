package services

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrInvalidFileType = errors.New("file type must be a PDF")
	ErrSaveFailed      = errors.New("failed to save file")
)

type DocumentService struct {
	fileStorage storage.Storage
	repository  *storage.DocumentRepository
}

func NewDocumentService(fileStorage storage.Storage, repository *storage.DocumentRepository) *DocumentService {
	return &DocumentService{
		fileStorage: fileStorage,
		repository:  repository,
	}
}

func (ds *DocumentService) UploadDocuments(cvFile, reportFile io.Reader, cvFilename, reportFilename string) (*models.UploadResponse, error) {
	if !isPDF(cvFilename) || !isPDF(reportFilename) {
		return nil, ErrInvalidFileType
	}

	if err := validatePDF(cvFile); err != nil {
		return nil, err
	}

	if err := validatePDF(reportFile); err != nil {
		return nil, err
	}

	cvDoc, err := ds.uploadDocument(cvFile, cvFilename, models.DocumentTypeCV)
	if err != nil {
		return nil, fmt.Errorf("failed to upload CV: %w", err)
	}

	reportDoc, err := ds.uploadDocument(reportFile, reportFilename, models.DocumentTypeProjectReport)
	if err != nil {
		ds.cleanupDocument(cvDoc)
		return nil, fmt.Errorf("failed to upload report: %w", err)
	}

	return &models.UploadResponse{
		CandidateCVID:   cvDoc.ID,
		ProjectReportID: reportDoc.ID,
	}, nil
}

func (ds *DocumentService) uploadDocument(file io.Reader, filename string, docType models.DocumentType) (*models.Document, error) {
	id := uuid.New().String()

	doc := &models.Document{
		ID:               id,
		Type:             docType,
		OriginalFilename: filename,
	}

	savedDoc, err := ds.fileStorage.Save(file, filename)
	if err != nil {
		return nil, ErrSaveFailed
	}

	doc.FilePath = savedDoc.FilePath

	if err := ds.repository.CreateDocument(doc); err != nil {
		os.Remove(doc.FilePath)
		return nil, fmt.Errorf("failed to save to database: %w", err)
	}

	return doc, nil

}

func (ds *DocumentService) cleanupDocument(doc *models.Document) {
	if doc.FilePath != "" {
		os.Remove(doc.FilePath)
	}

	ds.repository.DeleteDocument(doc.ID)
}

func isPDF(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".pdf" || ext == ".PDF"
}

func validatePDF(r io.Reader) error {
	buffer := make([]byte, 512)
	_, err := r.Read(buffer)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(buffer, []byte("%PDF-")) {
		return ErrInvalidFileType
	}
	if seeker, ok := r.(io.Seeker); ok {
		seeker.Seek(0, 0)
	}
	return nil
}
