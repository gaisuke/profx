package storage

import (
	"database/sql"
	"fmt"

	"github.com/gaisuke/profx/internal/models"
)

type DocumentRepository struct {
	db *sql.DB
}

func NewDocumentRepository(db *sql.DB) *DocumentRepository {
	return &DocumentRepository{db: db}
}

func (r *DocumentRepository) CreateDocument(doc *models.Document) error {
	query := `
		INSERT INTO documents (id, type, original_filename, file_path, uploaded_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, uploaded_at
	`

	err := r.db.QueryRow(
		query,
		doc.ID,
		doc.Type,
		doc.OriginalFilename,
		doc.FilePath,
	).Scan(&doc.ID, &doc.UploadedAt)

	if err != nil {
		return fmt.Errorf("failed to insert document: %w", err)
	}

	return nil
}

func (r *DocumentRepository) GetDocumentByID(id string) (*models.Document, error) {
	query := `
		SELECT id, type, original_filename, file_path, uploaded_at
		FROM documents
		WHERE id = $1
	`

	doc := &models.Document{}
	err := r.db.QueryRow(query, id).Scan(
		&doc.ID,
		&doc.Type,
		&doc.OriginalFilename,
		&doc.FilePath,
		&doc.UploadedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get document by id: %w", err)
	}

	return doc, nil
}

func (r *DocumentRepository) DeleteDocument(id string) error {
	query := `DELETE FROM documents WHERE id = $1`

	result, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("document not found: %s", id)
	}

	return nil
}
