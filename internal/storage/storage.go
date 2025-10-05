package storage

import (
	"io"

	"github.com/gaisuke/profx/internal/models"
)

// Storage defines the interface for document storage operations
type Storage interface {
	Save(file io.Reader, filename string) (*models.Document, error)
	GetPath(id string) string
}
