package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gaisuke/profx/internal/models"
)

// ErrCheckNotFound is returned when a check id is unknown or expired. Both
// cases are the same answer to the visitor: "that result is gone".
var ErrCheckNotFound = errors.New("check not found or expired")

type CekRepository struct {
	db *sql.DB
}

func NewCekRepository(db *sql.DB) *CekRepository {
	return &CekRepository{db: db}
}

func (r *CekRepository) Save(ctx context.Context, c *models.PublicCheck, ipHash string) error {
	payload, err := json.Marshal(c.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO public_checks (id, job_title, score, payload, ip_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7)`,
		c.ID, c.JobTitle, c.Score, string(payload), ipHash, c.CreatedAt, c.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert public check: %w", err)
	}
	return nil
}

// GetByID returns a check only while it is still valid. Expiry is enforced in
// the query, so a stale row can never be served even if cleanup has not run.
func (r *CekRepository) GetByID(ctx context.Context, id string) (*models.PublicCheck, error) {
	var (
		check   models.PublicCheck
		payload []byte
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, job_title, score, payload, created_at, expires_at
		FROM public_checks
		WHERE id = $1 AND expires_at > now()`, id).
		Scan(&check.ID, &check.JobTitle, &check.Score, &payload, &check.CreatedAt, &check.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCheckNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get public check: %w", err)
	}
	if err := json.Unmarshal(payload, &check.Payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return &check, nil
}

func (r *CekRepository) CountByIPSince(ctx context.Context, ipHash string, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM public_checks WHERE ip_hash = $1 AND created_at >= $2`, ipHash, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count checks by ip: %w", err)
	}
	return n, nil
}

func (r *CekRepository) CountSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM public_checks WHERE created_at >= $1`, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count checks: %w", err)
	}
	return n, nil
}

func (r *CekRepository) SaveInterest(ctx context.Context, id, checkID, contact, note string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO public_check_interest (id, check_id, contact, note, created_at)
		VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, ''), now())`, id, checkID, contact, note)
	if err != nil {
		return fmt.Errorf("insert interest: %w", err)
	}
	return nil
}

// DeleteExpired removes rows past their lifetime and reports how many went, so
// the retention promise in the UI is something an operator can verify.
func (r *CekRepository) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM public_checks WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired checks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
