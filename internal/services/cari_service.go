package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gaisuke/profx/internal/jobs"
	"github.com/gaisuke/profx/internal/llm"
	"github.com/gaisuke/profx/internal/models"
)

var (
	// ErrCariValidation is a request the visitor can fix.
	ErrCariValidation = errors.New("cari: permintaan belum memenuhi syarat")
	// ErrCariQuota is a spent allowance.
	ErrCariQuota = errors.New("cari: kuota gratis habis")
)

// CariStore is the persistence the matching service needs.
type CariStore interface {
	UpsertJobs(ctx context.Context, list []models.JobRow) error
	CreateRun(ctx context.Context, run *models.CariRun, cvFingerprint string, filters map[string]any, ipHash string) error
	SetRunProgress(ctx context.Context, runID string, total, done int) error
	FinishRun(ctx context.Context, runID, status, message string, total, done int) error
	SaveResult(ctx context.Context, runID, jobID string, item models.MatchItem) error
	GetRun(ctx context.Context, runID string) (*models.CariRun, error)
	CachedScore(ctx context.Context, cvFingerprint, jobID string, maxAge time.Duration) (*models.MatchJudgement, bool, error)
	SaveScore(ctx context.Context, cvFingerprint, jobID string, judgement models.MatchJudgement) error
	CountRunsByIPSince(ctx context.Context, ipHash string, since time.Time) (int, error)
	CountRunsSince(ctx context.Context, since time.Time) (int, error)
	DeleteExpiredRuns(ctx context.Context, now time.Time) (int64, error)
}

// CariConfig is the deployment's policy for the search feature.
type CariConfig struct {
	PerIPLimit  int           // searches per visitor per day
	GlobalLimit int           // searches per day across the deployment
	TTL         time.Duration // how long a finished search stays readable
	Keep        int           // how many postings get a model call
	MaxAgeDays  int           // ignore postings older than this
	Concurrency int           // parallel model calls inside one run
	CacheMaxAge time.Duration // reuse a judgement younger than this
	IPSalt      string
	// HomeCountry is the market the ranking treats as reachable when the visitor
	// does not say where they are.
	HomeCountry string
}

type CariService struct {
	store   CariStore
	llm     LLM
	sources []jobs.Source
	cfg     CariConfig
	loc     *time.Location

	queue chan *cariJob
	once  sync.Once
}

// cariJob carries the CV in memory only. Nothing about the CV is written down,
// which is why a restart drops in-flight searches instead of resuming them.
type cariJob struct {
	runID         string
	cvText        string
	cvFingerprint string
	filters       filters
}

type filters struct {
	Location   string
	RemoteOnly bool
	Keep       int
}

func NewCariService(store CariStore, llmClient LLM, sources []jobs.Source, cfg CariConfig) *CariService {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*60*60)
	}
	if cfg.TTL == 0 {
		cfg.TTL = 48 * time.Hour
	}
	if cfg.Keep <= 0 || cfg.Keep > 25 {
		cfg.Keep = 10
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 75
	}
	if cfg.Concurrency <= 0 || cfg.Concurrency > 8 {
		cfg.Concurrency = 4
	}
	if cfg.CacheMaxAge <= 0 {
		cfg.CacheMaxAge = 14 * 24 * time.Hour
	}
	return &CariService{
		store:   store,
		llm:     llmClient,
		sources: sources,
		cfg:     cfg,
		loc:     loc,
		queue:   make(chan *cariJob, 32),
	}
}

// Start launches the worker. One run at a time: the model calls inside a run are
// already parallel, and a queue of runs would multiply that concurrency across
// visitors.
func (s *CariService) Start(ctx context.Context) {
	s.once.Do(func() {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-s.queue:
					s.process(ctx, job)
				}
			}
		}()
	})
}

func (s *CariService) DayStart(now time.Time) time.Time {
	local := now.In(s.loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
}

func (s *CariService) HashIP(ip string) string {
	sum := sha256.Sum256([]byte(s.cfg.IPSalt + "|cari|" + ip))
	return hex.EncodeToString(sum[:])
}

// Fingerprint identifies a CV without keeping it.
func Fingerprint(cvText string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(cvText), " ")))
	return hex.EncodeToString(sum[:])
}

func (s *CariService) Quota(ctx context.Context, ip string) (models.CariQuota, error) {
	now := time.Now()
	start := s.DayStart(now)
	q := models.CariQuota{
		PerIPLimit:  s.cfg.PerIPLimit,
		GlobalLimit: s.cfg.GlobalLimit,
		ResetsAt:    start.Add(24 * time.Hour).Format(time.RFC3339),
	}
	if s.cfg.PerIPLimit > 0 {
		used, err := s.store.CountRunsByIPSince(ctx, s.HashIP(ip), start)
		if err != nil {
			return q, err
		}
		q.PerIPUsed = used
		q.Remaining = s.cfg.PerIPLimit - used
		if q.Remaining < 0 {
			q.Remaining = 0
		}
	} else {
		q.Remaining = -1
	}
	if s.cfg.GlobalLimit > 0 {
		used, err := s.store.CountRunsSince(ctx, start)
		if err != nil {
			return q, err
		}
		q.GlobalUsed = used
	}
	return q, nil
}

// Search validates, spends a quota slot, records the run, and queues it. The CV
// goes into memory with the queue entry and nowhere else.
func (s *CariService) Search(ctx context.Context, ip, cvText string, location string, remoteOnly bool, keep int) (*models.CariRun, error) {
	cvText = strings.TrimSpace(cvText)
	if len(cvText) < MinCVChars {
		return nil, validationErrorf(ErrCariValidation, "cv_text",
			"isi CV minimal %d karakter (sekarang %d)", MinCVChars, len(cvText))
	}
	if len(cvText) > MaxCVChars {
		return nil, validationErrorf(ErrCariValidation, "cv_text",
			"isi CV terlalu panjang (maks %d karakter)", MaxCVChars)
	}
	if keep <= 0 {
		keep = s.cfg.Keep
	}
	if keep > 25 {
		keep = 25
	}
	quota, err := s.Quota(ctx, ip)
	if err != nil {
		return nil, err
	}
	if quota.PerIPLimit > 0 && quota.Remaining <= 0 {
		return nil, fmt.Errorf("%w: batas %d pencarian gratis per orang per hari sudah terpakai, kuota direset %s",
			ErrCariQuota, quota.PerIPLimit, quota.ResetsAt)
	}
	if quota.GlobalLimit > 0 && quota.GlobalUsed >= quota.GlobalLimit {
		return nil, fmt.Errorf("%w: batas harian layanan ini sudah terpakai (%d pencarian), kuota direset %s",
			ErrCariQuota, quota.GlobalLimit, quota.ResetsAt)
	}

	now := time.Now()
	run := &models.CariRun{
		ID:        newCheckID(),
		Status:    "jalan",
		Pesan:     "mencari lowongan yang cocok…",
		CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.TTL),
	}
	fp := Fingerprint(cvText)
	filterBlob := map[string]any{"lokasi": location, "hanya_remote": remoteOnly, "jumlah": keep}
	if err := s.store.CreateRun(ctx, run, fp, filterBlob, s.HashIP(ip)); err != nil {
		return nil, err
	}
	select {
	case s.queue <- &cariJob{runID: run.ID, cvText: cvText, cvFingerprint: fp,
		filters: filters{Location: location, RemoteOnly: remoteOnly, Keep: keep}}:
	default:
		// The queue is full: say so instead of leaving a run stuck at "jalan".
		_ = s.store.FinishRun(ctx, run.ID, "gagal", "server sedang sibuk, coba lagi sebentar lagi", 0, 0)
		return nil, fmt.Errorf("server sedang sibuk, coba lagi sebentar lagi")
	}
	log.Printf("[cari %s] pencarian dimulai (cv %d karakter, filter lokasi=%q remote=%v jumlah=%d)",
		run.ID, len(cvText), location, remoteOnly, keep)
	return run, nil
}

// Result returns a stored run, in progress or finished.
func (s *CariService) Result(ctx context.Context, runID string) (*models.CariRun, error) {
	return s.store.GetRun(ctx, runID)
}

func (s *CariService) Cleanup(ctx context.Context) (int64, error) {
	n, err := s.store.DeleteExpiredRuns(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	if n > 0 {
		log.Printf("[cari] %d pencarian kedaluwarsa dihapus", n)
	}
	return n, nil
}

// process is one search: fetch, filter free, judge the survivors with the model.
func (s *CariService) process(ctx context.Context, job *cariJob) {
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	fetched := s.fetchAll(runCtx, job)
	log.Printf("[cari %s] %d lowongan diambil dari %d sumber", job.runID, len(fetched), len(s.sources))
	if len(fetched) == 0 {
		_ = s.store.FinishRun(ctx, job.runID, "gagal",
			"tidak ada lowongan yang bisa diambil dari sumbernya saat ini, coba lagi nanti", 0, 0)
		return
	}

	profile := jobs.NewProfile(job.cvText)
	// Prefer what the visitor can actually take: their stated location, else the
	// local market. Asked for "remote", reachability already treats remote roles
	// as first class.
	prefer := job.filters.Location
	if prefer == "" {
		prefer = s.cfg.HomeCountry
	}
	candidates := jobs.FilterAndRank(profile, fetched, job.filters.Keep, s.cfg.MaxAgeDays, prefer)
	candidates = applyFilters(candidates, job.filters)
	if len(candidates) == 0 {
		_ = s.store.FinishRun(ctx, job.runID, "selesai",
			fmt.Sprintf("tidak ada lowongan yang cukup dekat dengan profilmu dari %d lowongan yang diperiksa", len(fetched)), 0, 0)
		return
	}
	_ = s.store.SetRunProgress(ctx, job.runID, len(candidates), 0)

	// Keep the postings: public data, and the result page needs the link.
	rows := make([]models.JobRow, 0, len(candidates))
	for _, c := range candidates {
		rows = append(rows, toRow(c))
	}
	if err := s.store.UpsertJobs(ctx, rows); err != nil {
		log.Printf("[cari %s] gagal menyimpan lowongan: %v", job.runID, err)
	}

	var (
		mu     sync.Mutex
		done   int
		scored int
		cached int
		wg     sync.WaitGroup
		sem    = make(chan struct{}, s.cfg.Concurrency)
	)
	for i := range candidates {
		wg.Add(1)
		go func(c jobs.Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			jobID := jobIDFor(c)
			judgement, fromCache, err := s.judge(runCtx, job.cvFingerprint, jobID, job.cvText, c)
			if err != nil {
				log.Printf("[cari %s] gagal menilai %s: %v", job.runID, c.Title, err)
			} else {
				item := models.MatchItem{
					Skor:   judgement.Skor,
					Alasan: judgement.Alasan,
					Celah:  judgement.Celah,
					Job:    summarise(c),
				}
				if err := s.store.SaveResult(ctx, job.runID, jobID, item); err != nil {
					log.Printf("[cari %s] gagal menyimpan hasil %s: %v", job.runID, c.Title, err)
				} else {
					mu.Lock()
					scored++
					if fromCache {
						cached++
					}
					mu.Unlock()
				}
			}
			mu.Lock()
			done++
			progress := done
			mu.Unlock()
			_ = s.store.SetRunProgress(ctx, job.runID, len(candidates), progress)
		}(candidates[i])
	}
	wg.Wait()

	message := fmt.Sprintf("%d lowongan dinilai dari %d yang diperiksa", scored, len(fetched))
	if cached > 0 {
		message += fmt.Sprintf(" (%d dari cache, tanpa panggilan model)", cached)
	}
	status := "selesai"
	if scored == 0 {
		status, message = "gagal", "tidak ada lowongan yang berhasil dinilai, coba lagi sebentar lagi"
	}
	_ = s.store.FinishRun(ctx, job.runID, status, message, len(candidates), scored)
	log.Printf("[cari %s] %s: %s", job.runID, status, message)
}

// fetchAll queries every source in parallel with its own timeout, so one slow
// board cannot hold up a search.
func (s *CariService) fetchAll(ctx context.Context, job *cariJob) []jobs.Job {
	var (
		mu  sync.Mutex
		out []jobs.Job
		wg  sync.WaitGroup
	)
	keywords := jobs.NewProfile(job.cvText).Tokens()
	if len(keywords) > 12 {
		keywords = keywords[:12]
	}
	for _, src := range s.sources {
		wg.Add(1)
		go func(src jobs.Source) {
			defer wg.Done()
			srcCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
			defer cancel()
			list, err := src.Fetch(srcCtx, jobs.Query{Keywords: keywords, Location: job.filters.Location})
			if err != nil {
				log.Printf("[cari %s] sumber %s gagal: %v", job.runID, src.Name(), err)
				return
			}
			log.Printf("[cari %s] sumber %s: %d lowongan", job.runID, src.Name(), len(list))
			mu.Lock()
			out = append(out, list...)
			mu.Unlock()
		}(src)
	}
	wg.Wait()
	return out
}

// judge scores one posting, using the cache when this CV has been judged against
// the same posting before.
func (s *CariService) judge(ctx context.Context, cvFingerprint, jobID, cvText string, j jobs.Job) (models.MatchJudgement, bool, error) {
	if cached, ok, err := s.store.CachedScore(ctx, cvFingerprint, jobID, s.cfg.CacheMaxAge); err == nil && ok {
		return *cached, true, nil
	}
	judgement, err := s.judgeText(ctx, cvText, j)
	if err != nil {
		return models.MatchJudgement{}, false, err
	}
	if err := s.store.SaveScore(ctx, cvFingerprint, jobID, *judgement); err != nil {
		log.Printf("[cari] gagal menyimpan cache skor: %v", err)
	}
	return *judgement, false, nil
}

// judgeText is the pure part: prompt, model call, parse, validate.
func (s *CariService) judgeText(ctx context.Context, cvText string, j jobs.Job) (*models.MatchJudgement, error) {
	prompt := buildCariPrompt(cvText, j.Title, j.Company, j.Location, j.Tags, j.Description)
	response, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("panggilan model gagal: %w", err)
	}
	var judgement models.MatchJudgement
	if err := llm.ParseJSONResponse(response, &judgement); err != nil {
		return nil, fmt.Errorf("jawaban model tidak bisa dibaca: %w (jawaban: %s)", err, truncate(response))
	}
	if err := validateJudgement(&judgement); err != nil {
		return nil, fmt.Errorf("hasil tidak masuk akal: %w (jawaban: %s)", err, truncate(response))
	}
	return &judgement, nil
}

func validateJudgement(j *models.MatchJudgement) error {
	if j.Skor < 0 || j.Skor > 100 {
		return fmt.Errorf("skor di luar 0-100: %d", j.Skor)
	}
	if len([]rune(j.Alasan)) < 40 {
		return fmt.Errorf("alasan terlalu pendek (%d karakter)", len([]rune(j.Alasan)))
	}
	if len(j.Celah) > 3 {
		j.Celah = j.Celah[:3]
	}
	for i, c := range j.Celah {
		if len([]rune(strings.TrimSpace(c))) < 5 {
			return fmt.Errorf("celah ke-%d terlalu pendek", i+1)
		}
	}
	return nil
}

// applyFilters is the real filtering. Sources ignore what they cannot do; the
// service is what actually honours the visitor's choices.
func applyFilters(list []jobs.Job, f filters) []jobs.Job {
	out := make([]jobs.Job, 0, len(list))
	location := strings.ToLower(strings.TrimSpace(f.Location))
	for _, j := range list {
		if f.RemoteOnly && !j.Remote {
			continue
		}
		if location != "" {
			haystack := strings.ToLower(j.Location + " " + j.Title + " " + strings.Join(j.Tags, " "))
			if !strings.Contains(haystack, location) && !(j.Remote && strings.Contains(location, "remote")) {
				continue
			}
		}
		out = append(out, j)
	}
	return out
}

func toRow(j jobs.Job) models.JobRow {
	return models.JobRow{
		ID:          jobIDFor(j),
		Source:      j.Source,
		ExternalID:  j.ExternalID,
		Title:       j.Title,
		Company:     j.Company,
		Location:    j.Location,
		Remote:      j.Remote,
		URL:         j.URL,
		Description: j.Description,
		Tags:        j.Tags,
		PublishedAt: j.PublishedAt,
	}
}

func jobIDFor(j jobs.Job) string {
	// Same recipe as storage.JobID, kept here so the service can key its cache
	// without importing storage.
	sum := sha256.Sum256([]byte(strings.ToLower(j.Source) + "|" + strings.ToLower(j.ExternalID)))
	return hex.EncodeToString(sum[:16])
}

func summarise(j jobs.Job) models.JobSummary {
	return models.JobSummary{
		Title:       j.Title,
		Company:     j.Company,
		Location:    j.Location,
		Remote:      j.Remote,
		Source:      j.Source,
		URL:         j.URL,
		PublishedAt: j.PublishedAt,
	}
}
