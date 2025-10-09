package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gaisuke/profx/internal/models"
	"github.com/google/uuid"
)

type Storage interface {
	Save(file io.Reader, filename string) (*models.Document, error)
}

type FileStorage struct {
	uploadDir string
}

func NewFileStorage(uploadDir string) (*FileStorage, error) {
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	return &FileStorage{
		uploadDir: uploadDir,
	}, nil

}

func (fs *FileStorage) Save(file io.Reader, filename string) (*models.Document, error) {
	id := uuid.New().String()
	ext := filepath.Ext(filename)
	newFilename := fmt.Sprintf("%s%s", id, ext)
	filePath := filepath.Join(fs.uploadDir, newFilename)

	dst, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(filePath)
		return nil, fmt.Errorf("failed to save file: %w", err)
	}

	return &models.Document{
		ID:               id,
		OriginalFilename: filename,
		FilePath:         filePath,
	}, nil
}
