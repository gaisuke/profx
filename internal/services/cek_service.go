package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gaisuke/profx/internal/llm"
	"github.com/gaisuke/profx/internal/models"
	"github.com/ledongthuc/pdf"
)

// Input limits. The job description becomes the rubric, so it has to be a real
// posting, not a line of text; the CV has to be long enough to judge at all.
const (
	MinJobDescChars = 80
	MaxJobDescChars = 12000
	MinCVChars      = 200
	MaxCVChars      = 20000
)

var (
	// ErrCekValidation means the request itself is unusable (too short, missing
	// both CV forms). The message is written for the visitor, not for a log.
	ErrCekValidation = errors.New("cek: input belum memenuhi syarat")
	// ErrCekQuota means the free allowance is spent. Distinct from a validation
	// error so the page can say "coba lagi besok" instead of "perbaiki isian".
	ErrCekQuota = errors.New("cek: kuota gratis habis")
)

// CekStore is the persistence this service needs. Declared here, next to its
// consumer, so the service can be tested against a fake with no database.
type CekStore interface {
	Save(ctx context.Context, c *models.PublicCheck, ipHash string) error
	GetByID(ctx context.Context, id string) (*models.PublicCheck, error)
	CountByIPSince(ctx context.Context, ipHash string, since time.Time) (int, error)
	CountSince(ctx context.Context, since time.Time) (int, error)
	SaveInterest(ctx context.Context, id, checkID, contact, note string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// CekConfig is the deployment's policy: how much free work one visitor gets,
// how much the whole endpoint may spend in a day, and how long a result stays
// readable.
type CekConfig struct {
	PerIPLimit  int
	GlobalLimit int
	TTL         time.Duration
	IPSalt      string
	PaidOpen    bool
}

type CekService struct {
	store CekStore
	llm   LLM
	cfg   CekConfig
	loc   *time.Location
}

func NewCekService(store CekStore, llmClient LLM, cfg CekConfig) *CekService {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*60*60)
	}
	// A zero TTL means "use the default"; a negative one is respected, which is
	// how a test (or an operator in a hurry) makes results expire immediately.
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	return &CekService{store: store, llm: llmClient, cfg: cfg, loc: loc}
}

// DayStart is midnight WIB: the reset the visitor can reason about.
func (s *CekService) DayStart(now time.Time) time.Time {
	local := now.In(s.loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
}

// HashIP keeps the ability to count per visitor without keeping the visitor's
// address. The salt is deployment config, so the same IP hashes differently on
// another host.
func (s *CekService) HashIP(ip string) string {
	sum := sha256.Sum256([]byte(s.cfg.IPSalt + "|" + ip))
	return hex.EncodeToString(sum[:])
}

// Quota reports what is left, for the page and for the refusal message.
func (s *CekService) Quota(ctx context.Context, ip string) (models.CekQuota, error) {
	now := time.Now()
	start := s.DayStart(now)
	q := models.CekQuota{
		PerIPLimit:  s.cfg.PerIPLimit,
		GlobalLimit: s.cfg.GlobalLimit,
		ResetsAt:    start.Add(24 * time.Hour).Format(time.RFC3339),
		PaidOpen:    s.cfg.PaidOpen,
	}
	if s.cfg.PerIPLimit > 0 {
		used, err := s.store.CountByIPSince(ctx, s.HashIP(ip), start)
		if err != nil {
			return q, err
		}
		q.PerIPUsed = used
		q.Remaining = s.cfg.PerIPLimit - used
		if q.Remaining < 0 {
			q.Remaining = 0
		}
	} else {
		q.Remaining = -1 // unlimited per visitor
	}
	if s.cfg.GlobalLimit > 0 {
		used, err := s.store.CountSince(ctx, start)
		if err != nil {
			return q, err
		}
		q.GlobalUsed = used
	}
	return q, nil
}

// Evaluate runs one free check. The CV text is used and dropped: only the score
// and the findings are stored.
func (s *CekService) Evaluate(ctx context.Context, ip, jobTitle, jobDesc, cvText string) (*models.PublicCheck, error) {
	jobDesc = strings.TrimSpace(jobDesc)
	cvText = strings.TrimSpace(cvText)
	if err := validateCekInput(jobDesc, cvText); err != nil {
		return nil, err
	}
	quota, err := s.Quota(ctx, ip)
	if err != nil {
		return nil, err
	}
	if quota.PerIPLimit > 0 && quota.Remaining <= 0 {
		return nil, fmt.Errorf("%w: batas %d cek gratis per orang per hari sudah terpakai, kuota direset %s",
			ErrCekQuota, quota.PerIPLimit, quota.ResetsAt)
	}
	if quota.GlobalLimit > 0 && quota.GlobalUsed >= quota.GlobalLimit {
		return nil, fmt.Errorf("%w: batas harian layanan ini sudah terpakai (%d cek), kuota direset %s",
			ErrCekQuota, quota.GlobalLimit, quota.ResetsAt)
	}

	if jobTitle == "" {
		jobTitle = titleFromJobDesc(jobDesc)
	}
	result, err := s.evaluateText(ctx, jobTitle, jobDesc, cvText)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	check := &models.PublicCheck{
		ID:        newCheckID(),
		JobTitle:  jobTitle,
		Score:     result.Skor,
		Payload:   *result,
		CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.TTL),
	}
	// The store gets the hash, never the address.
	if err := s.store.Save(ctx, check, s.HashIP(ip)); err != nil {
		return nil, err
	}
	log.Printf("[cek %s] skor %d (lowongan %q, cv %d karakter)", check.ID, check.Score, jobTitle, len(cvText))
	return check, nil
}

// evaluateText is the pure part: prompt, model call, parse, validate. Split out
// so tests drive it with a scripted model and no HTTP.
func (s *CekService) evaluateText(ctx context.Context, jobTitle, jobDesc, cvText string) (*models.CekResult, error) {
	prompt := buildCekPrompt(jobTitle, jobDesc, cvText)
	response, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("panggilan model gagal: %w", err)
	}
	var result models.CekResult
	if err := llm.ParseJSONResponse(response, &result); err != nil {
		return nil, fmt.Errorf("jawaban model tidak bisa dibaca: %w (jawaban: %s)", err, truncate(response))
	}
	if err := validateCekResult(&result); err != nil {
		return nil, fmt.Errorf("hasil tidak masuk akal: %w (jawaban: %s)", err, truncate(response))
	}
	return &result, nil
}

// Result returns a stored check while it is still valid.
func (s *CekService) Result(ctx context.Context, id string) (*models.PublicCheck, error) {
	return s.store.GetByID(ctx, id)
}

// Interest records demand for the paid part before it exists. A waitlist is
// honest; a dead "beli sekarang" button is not.
func (s *CekService) Interest(ctx context.Context, checkID, contact, note string) error {
	contact = strings.TrimSpace(contact)
	if len(contact) < 5 {
		return validationErrorf(ErrCekValidation, "contact",
			"isi kontak (WhatsApp atau email) supaya bisa dikabari")
	}
	if len(contact) > 200 {
		contact = contact[:200]
	}
	if len(note) > 500 {
		note = note[:500]
	}
	return s.store.SaveInterest(ctx, newCheckID(), checkID, contact, note)
}

// Cleanup deletes results past their lifetime. Called on a ticker, and its
// count is logged so the retention promise is verifiable.
func (s *CekService) Cleanup(ctx context.Context) (int64, error) {
	n, err := s.store.DeleteExpired(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	if n > 0 {
		log.Printf("[cek] %d hasil kedaluwarsa dihapus", n)
	}
	return n, nil
}

// ExtractPDFText pulls text out of an uploaded CV without writing it to disk:
// the bytes are read, the text is judged, and the file never lands anywhere.
func ExtractPDFText(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("berkas CV kosong")
	}
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("PDF tidak bisa dibaca: %w", err)
	}
	var sb strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		sb.WriteString(text)
		sb.WriteString("\n")
	}
	out := strings.TrimSpace(sb.String())
	if len(out) < MinCVChars {
		return out, fmt.Errorf("teks CV terlalu pendek (%d karakter) — kalau CV-mu hasil scan gambar, tempel teksnya secara manual", len(out))
	}
	return out, nil
}

func validateCekInput(jobDesc, cvText string) error {
	if len(jobDesc) < MinJobDescChars {
		return validationErrorf(ErrCekValidation, "job_desc",
			"deskripsi lowongan minimal %d karakter supaya bisa dinilai dengan jujur", MinJobDescChars)
	}
	if len(jobDesc) > MaxJobDescChars {
		return validationErrorf(ErrCekValidation, "job_desc",
			"deskripsi lowongan terlalu panjang (maks %d karakter)", MaxJobDescChars)
	}
	if len(cvText) < MinCVChars {
		return validationErrorf(ErrCekValidation, "cv_text",
			"isi CV minimal %d karakter (sekarang %d)", MinCVChars, len(cvText))
	}
	if len(cvText) > MaxCVChars {
		return validationErrorf(ErrCekValidation, "cv_text",
			"isi CV terlalu panjang (maks %d karakter)", MaxCVChars)
	}
	return nil
}

func validateCekResult(r *models.CekResult) error {
	if r.Skor < 0 || r.Skor > 100 {
		return fmt.Errorf("skor di luar 0-100: %d", r.Skor)
	}
	if len([]rune(r.Ringkasan)) < 40 {
		return fmt.Errorf("ringkasan terlalu pendek (%d karakter)", len([]rune(r.Ringkasan)))
	}
	if len(r.Celah) != 3 {
		return fmt.Errorf("jumlah celah harus 3, dapat %d", len(r.Celah))
	}
	for i, g := range r.Celah {
		if len([]rune(g.Bagian)) < 3 || len([]rune(g.Masalah)) < 10 || len([]rune(g.Perbaikan)) < 10 {
			return fmt.Errorf("celah ke-%d tidak lengkap", i+1)
		}
	}
	c := r.Contoh
	if len([]rune(c.Sebelum)) < 10 || len([]rune(c.Sesudah)) < 10 || len([]rune(c.Alasan)) < 10 {
		return fmt.Errorf("contoh perbaikan tidak lengkap")
	}
	return nil
}

// titleFromJobDesc guesses a short label for the result page: the first line
// that looks like a title, capped so it fits a heading.
func titleFromJobDesc(jobDesc string) string {
	for _, line := range strings.Split(jobDesc, "\n") {
		line = strings.TrimSpace(line)
		if len([]rune(line)) >= 4 {
			runes := []rune(line)
			if len(runes) > 80 {
				runes = runes[:80]
			}
			return strings.TrimSpace(string(runes))
		}
	}
	return "Lowongan"
}

// newCheckID returns an unguessable id: 80 bits of randomness in base32. The id
// is the only thing protecting a result page, so it must not be enumerable.
func newCheckID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not a case worth continuing through: a
		// predictable id would expose other people's results.
		panic("cek: crypto/rand tidak tersedia: " + err.Error())
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}
