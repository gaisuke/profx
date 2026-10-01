package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gaisuke/profx/internal/models"
	"github.com/lib/pq"
)

// ErrRunNotFound is the answer for an unknown or expired search.
var ErrRunNotFound = errors.New("pencarian tidak ditemukan atau sudah kedaluwarsa")

type CariRepository struct {
	db *sql.DB
}

func NewCariRepository(db *sql.DB) *CariRepository { return &CariRepository{db: db} }

// JobID is the stable identifier for a posting: the same job on the same board
// keeps the same id across runs, which is what makes the score cache usable.
func JobID(source, externalID string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(source) + "|" + strings.ToLower(externalID)))
	return hex.EncodeToString(sum[:16])
}

func (r *CariRepository) UpsertJobs(ctx context.Context, list []models.JobRow) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	for _, j := range list {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO job_postings (id, source, external_id, title, company, location, remote, url, description, tags, published_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (source, external_id) DO UPDATE SET
				title = EXCLUDED.title, company = EXCLUDED.company, location = EXCLUDED.location,
				remote = EXCLUDED.remote, url = EXCLUDED.url, description = EXCLUDED.description,
				tags = EXCLUDED.tags, published_at = EXCLUDED.published_at, fetched_at = now()`,
			j.ID, j.Source, j.ExternalID, j.Title, j.Company, j.Location, j.Remote, j.URL,
			j.Description, pq.Array(j.Tags), nullableTime(j.PublishedAt)); err != nil {
			return fmt.Errorf("upsert job %s: %w", j.ID, err)
		}
	}
	return tx.Commit()
}

func (r *CariRepository) CreateRun(ctx context.Context, run *models.CariRun, cvFingerprint string, filters map[string]any, ipHash string) error {
	blob, err := json.Marshal(filters)
	if err != nil {
		return fmt.Errorf("marshal filters: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO match_runs (id, cv_fingerprint, status, pesan, total, selesai, filters, ip_hash, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10)`,
		run.ID, cvFingerprint, run.Status, run.Pesan, run.Total, run.Selesai,
		string(blob), ipHash, run.CreatedAt, run.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

// SetRunProgress records how far along a run is, so the page can show honest
// progress instead of a spinner.
func (r *CariRepository) SetRunProgress(ctx context.Context, runID string, total, done int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE match_runs SET total = $2, selesai = $3 WHERE id = $1`, runID, total, done)
	if err != nil {
		return fmt.Errorf("update progress: %w", err)
	}
	return nil
}

func (r *CariRepository) FinishRun(ctx context.Context, runID, status, message string, total, done int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE match_runs SET status = $2, pesan = $3, total = $4, selesai = $5 WHERE id = $1`,
		runID, status, message, total, done)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

func (r *CariRepository) SaveResult(ctx context.Context, runID, jobID string, item models.MatchItem) error {
	blob, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal match item: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO match_results (run_id, job_id, score, payload)
		VALUES ($1,$2,$3,$4::jsonb)
		ON CONFLICT (run_id, job_id) DO UPDATE SET score = EXCLUDED.score, payload = EXCLUDED.payload`,
		runID, jobID, item.Skor, string(blob))
	if err != nil {
		return fmt.Errorf("insert result: %w", err)
	}
	return nil
}

// GetRun returns a run with its results, best score first, while it is alive.
func (r *CariRepository) GetRun(ctx context.Context, runID string) (*models.CariRun, error) {
	var (
		run models.CariRun
		msg sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, status, pesan, total, selesai, created_at, expires_at
		FROM match_runs WHERE id = $1 AND expires_at > now()`, runID).
		Scan(&run.ID, &run.Status, &msg, &run.Total, &run.Selesai, &run.CreatedAt, &run.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	run.Pesan = msg.String

	rows, err := r.db.QueryContext(ctx, `
		SELECT payload FROM match_results WHERE run_id = $1 ORDER BY score DESC, created_at ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("get results: %w", err)
	}
	defer rows.Close()
	run.Hasil = []models.MatchItem{}
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		var item models.MatchItem
		if err := json.Unmarshal(blob, &item); err != nil {
			return nil, fmt.Errorf("unmarshal result: %w", err)
		}
		run.Hasil = append(run.Hasil, item)
	}
	return &run, rows.Err()
}

// CachedScore returns a previous judgement for this CV and posting.
func (r *CariRepository) CachedScore(ctx context.Context, cvFingerprint, jobID string, maxAge time.Duration) (*models.MatchJudgement, bool, error) {
	var blob []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT payload FROM job_scores
		WHERE cv_fingerprint = $1 AND job_id = $2 AND created_at > $3`,
		cvFingerprint, jobID, time.Now().Add(-maxAge)).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get cached score: %w", err)
	}
	var j models.MatchJudgement
	if err := json.Unmarshal(blob, &j); err != nil {
		return nil, false, nil // an unreadable cache entry is a miss, not an error
	}
	return &j, true, nil
}

func (r *CariRepository) SaveScore(ctx context.Context, cvFingerprint, jobID string, j models.MatchJudgement) error {
	blob, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("marshal judgement: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO job_scores (cv_fingerprint, job_id, score, payload)
		VALUES ($1,$2,$3,$4::jsonb)
		ON CONFLICT (cv_fingerprint, job_id) DO UPDATE SET score = EXCLUDED.score, payload = EXCLUDED.payload, created_at = now()`,
		cvFingerprint, jobID, j.Skor, string(blob))
	if err != nil {
		return fmt.Errorf("save score: %w", err)
	}
	return nil
}

func (r *CariRepository) CountRunsByIPSince(ctx context.Context, ipHash string, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM match_runs WHERE ip_hash = $1 AND created_at >= $2`, ipHash, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count runs by ip: %w", err)
	}
	return n, nil
}

func (r *CariRepository) CountRunsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM match_runs WHERE created_at >= $1`, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count runs: %w", err)
	}
	return n, nil
}

// DeleteExpiredRuns drops finished searches past their lifetime. Job postings
// and the score cache are kept: postings are public, and the cache is keyed by a
// CV hash, not by a person.
func (r *CariRepository) DeleteExpiredRuns(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM match_runs WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired runs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// EnabledSources lists which boards have postings stored, for the ops endpoint.
func (r *CariRepository) EnabledSources(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT source, count(*) FROM job_postings GROUP BY source ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, rows.Err()
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
