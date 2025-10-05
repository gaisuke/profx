package storage

import (
	"io"
	"os"
	"path/filepath"

	"github.com/gaisuke/profx/internal/models"
	"github.com/google/uuid"
)

// FileStorage implements Storage interface for local file system
type FileStorage struct {
	baseDir string
}

// NewFileStorage creates a new FileStorage instance
func NewFileStorage(baseDir string) (*FileStorage, error) {
	// Ensure directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}
	
	return &FileStorage{
		baseDir: baseDir,
	}, nil
}

// Save stores a file and returns a Document with its ID
func (fs *FileStorage) Save(file io.Reader, filename string) (*models.Document, error) {
	// Generate unique ID
	id := uuid.New().String()
	
	// Determine file extension
	ext := filepath.Ext(filename)
	path := filepath.Join(fs.baseDir, id+ext)
	
	// Create destination file
	dst, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	
	// Copy file content
	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(path) // Cleanup on error
		return nil, err
	}
	
	return &models.Document{
		ID:       id,
		Filename: filename,
		Path:     path,
	}, nil
}

// GetPath returns the file path for a given document ID
func (fs *FileStorage) GetPath(id string) string {
	return filepath.Join(fs.baseDir, id+".pdf")
}
