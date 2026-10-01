package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gaisuke/profx/internal/jobs"
	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/storage"
)

// fakeSource is a job board that answers with whatever the test scripted.
type fakeSource struct {
	name string
	list []jobs.Job
	err  error
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Fetch(ctx context.Context, q jobs.Query) ([]jobs.Job, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

// fakeCariStore records everything the service does, in memory.
type fakeCariStore struct {
	mu       sync.Mutex
	jobs     map[string]models.JobRow
	runs     map[string]*models.CariRun
	filters  map[string]string
	ipHashes []string
	scores   map[string]models.MatchJudgement // cache key
	total    int
}

func newFakeCariStore() *fakeCariStore {
	return &fakeCariStore{
		jobs:    map[string]models.JobRow{},
		runs:    map[string]*models.CariRun{},
		filters: map[string]string{},
		scores:  map[string]models.MatchJudgement{},
	}
}

func (f *fakeCariStore) UpsertJobs(ctx context.Context, list []models.JobRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range list {
		f.jobs[j.ID] = j
	}
	return nil
}

func (f *fakeCariStore) CreateRun(ctx context.Context, run *models.CariRun, cvFingerprint string, filters map[string]any, ipHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	blob := fmt.Sprintf("fp=%s", cvFingerprint)
	for k, v := range filters {
		blob += fmt.Sprintf(" %s=%v", k, v)
	}
	f.runs[run.ID] = &models.CariRun{ID: run.ID, Status: run.Status, Pesan: run.Pesan,
		CreatedAt: run.CreatedAt, ExpiresAt: run.ExpiresAt, Hasil: []models.MatchItem{}}
	f.filters[run.ID] = blob
	f.ipHashes = append(f.ipHashes, ipHash)
	f.total++
	return nil
}

func (f *fakeCariStore) SetRunProgress(ctx context.Context, runID string, total, done int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.runs[runID]; r != nil {
		r.Total, r.Selesai = total, done
	}
	return nil
}

func (f *fakeCariStore) FinishRun(ctx context.Context, runID, status, message string, total, done int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.runs[runID]; r != nil {
		r.Status, r.Pesan, r.Total, r.Selesai = status, message, total, done
	}
	return nil
}

func (f *fakeCariStore) SaveResult(ctx context.Context, runID, jobID string, item models.MatchItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.runs[runID]; r != nil {
		r.Hasil = append(r.Hasil, item)
	}
	return nil
}

func (f *fakeCariStore) GetRun(ctx context.Context, runID string) (*models.CariRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[runID]
	if !ok {
		return nil, storage.ErrRunNotFound
	}
	return r, nil
}

func (f *fakeCariStore) CachedScore(ctx context.Context, cvFingerprint, jobID string, maxAge time.Duration) (*models.MatchJudgement, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.scores[cvFingerprint+"|"+jobID]
	if !ok {
		return nil, false, nil
	}
	return &j, true, nil
}

func (f *fakeCariStore) SaveScore(ctx context.Context, cvFingerprint, jobID string, judgement models.MatchJudgement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scores[cvFingerprint+"|"+jobID] = judgement
	return nil
}

func (f *fakeCariStore) CountRunsByIPSince(ctx context.Context, ipHash string, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.ipHashes {
		if h == ipHash {
			n++
		}
	}
	return n, nil
}

func (f *fakeCariStore) CountRunsSince(ctx context.Context, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total, nil
}

func (f *fakeCariStore) DeleteExpiredRuns(ctx context.Context, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for id, r := range f.runs {
		if !r.ExpiresAt.After(now) {
			delete(f.runs, id)
			n++
		}
	}
	return n, nil
}

const (
	cariCV = `Budi Santoso, Backend Engineer di Jakarta.
Empat tahun membangun layanan Go untuk pembayaran berlatensi rendah, menangani ribuan transaksi per detik.
Bekerja dengan PostgreSQL untuk basis data besar dan Kubernetes di lingkungan produksi, termasuk penanganan insiden.
Keahlian: Go, PostgreSQL, Kubernetes, gRPC, observability, Docker, pengujian otomatis.`

	cariJudgement = `{"skor":74,"alasan":"CV ini menunjukkan pengalaman Go dan PostgreSQL yang relevan dengan lowongan, meski Kubernetes hanya disebut tanpa konteks produksi.","celah":["Kubernetes belum dibuktikan di lingkungan produksi"]}`
)

func judgementJSON(skor int, alasan string) string {
	return fmt.Sprintf(`{"skor":%d,"alasan":"%s","celah":["celah pertama yang konkret"]}`, skor, alasan)
}

// testCariService wires the service to fakes. Concurrency is 1 so the scripted
// model replies arrive in a predictable order.
func testCariService(store CariStore, llm LLM, sources []jobs.Source, cfg CariConfig) *CariService {
	cfg.Concurrency = 1
	if cfg.IPSalt == "" {
		cfg.IPSalt = "test-salt"
	}
	return NewCariService(store, llm, sources, cfg)
}

func backendJob(id, title string) jobs.Job {
	return jobs.Job{
		Source: "uji", ExternalID: id, Title: title, Company: "PT Uji",
		Location: "Jakarta, Indonesia", URL: "https://contoh.test/" + id,
		Description: "Kami mencari backend engineer dengan Go, PostgreSQL, dan Kubernetes.",
		Tags:        []string{"go", "postgresql"}, PublishedAt: time.Now().AddDate(0, 0, -2),
	}
}

func TestSearchRejectsAShortCV(t *testing.T) {
	svc := testCariService(newFakeCariStore(), &fakeLLM{}, nil, CariConfig{PerIPLimit: 2})
	if _, err := svc.Search(context.Background(), "1.2.3.4", "Nama: Budi", "", false, 10); !errors.Is(err, ErrCariValidation) {
		t.Fatalf("err = %v, mau ErrCariValidation", err)
	}
}

func TestSearchEnforcesThePerVisitorQuota(t *testing.T) {
	store := newFakeCariStore()
	svc := testCariService(store, &fakeLLM{}, nil, CariConfig{PerIPLimit: 1})
	if _, err := svc.Search(context.Background(), "9.9.9.9", cariCV, "", false, 5); err != nil {
		t.Fatalf("pencarian pertama gagal: %v", err)
	}
	_, err := svc.Search(context.Background(), "9.9.9.9", cariCV, "", false, 5)
	if !errors.Is(err, ErrCariQuota) {
		t.Fatalf("pencarian kedua err = %v, mau ErrCariQuota", err)
	}
	if _, err := svc.Search(context.Background(), "8.8.8.8", cariCV, "", false, 5); err != nil {
		t.Fatalf("pengunjung lain ditolak: %v", err)
	}
}

func TestTheCVRidesInMemoryAndOnlyAFingerprintIsWritten(t *testing.T) {
	store := newFakeCariStore()
	svc := testCariService(store, &fakeLLM{}, nil, CariConfig{PerIPLimit: 2})
	run, err := svc.Search(context.Background(), "1.2.3.4", cariCV, "jakarta", true, 7)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	stored := store.filters[run.ID]
	if strings.Contains(stored, "Budi") || strings.Contains(stored, "PostgreSQL") {
		t.Fatalf("isi CV ikut tersimpan: %q", stored)
	}
	fp := Fingerprint(cariCV)
	if len(fp) != 64 || strings.Contains(fp, " ") {
		t.Fatalf("fingerprint tidak seperti sha256 hex: %q", fp)
	}
	if !strings.Contains(stored, "fp="+fp) {
		t.Fatalf("fingerprint tidak tersimpan: %q", stored)
	}
	// The fingerprint identifies a CV without being one: whitespace changes do
	// not alter it, and it cannot be read back.
	if Fingerprint("  "+cariCV+"  ") != fp {
		t.Fatal("fingerprint berubah hanya karena spasi di ujung")
	}
}

func TestProcessRanksJudgesAndFinishesTheRun(t *testing.T) {
	store := newFakeCariStore()
	llm := &fakeLLM{responses: []string{
		judgementJSON(91, "Sangat cocok: pengalaman Go dan PostgreSQL pelamar persis yang diminta."),
		judgementJSON(38, "Kurang cocok: lowongan ini meminta hal yang tidak ada di CV pelamar."),
	}}
	src := &fakeSource{name: "uji", list: []jobs.Job{
		backendJob("1", "Backend Engineer (Go)"),
		backendJob("2", "Backend Engineer - Data Platform Kubernetes PostgreSQL"),
	}}
	svc := testCariService(store, llm, []jobs.Source{src}, CariConfig{PerIPLimit: 2, Keep: 5})

	run, err := svc.Search(context.Background(), "1.2.3.4", cariCV, "", false, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	pending, _ := store.GetRun(context.Background(), run.ID)
	svc.process(context.Background(), &cariJob{
		runID: run.ID, cvText: cariCV, cvFingerprint: Fingerprint(cariCV),
		filters: filters{Keep: 5},
	})

	got, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != "selesai" {
		t.Fatalf("status = %q (%s)", got.Status, got.Pesan)
	}
	if len(got.Hasil) != 2 {
		t.Fatalf("hasil = %d, mau 2", len(got.Hasil))
	}
	if got.Selesai != 2 || got.Total != 2 {
		t.Fatalf("progres = %d/%d", got.Selesai, got.Total)
	}
	for _, item := range got.Hasil {
		if item.Skor <= 0 || item.Alasan == "" || item.Job.URL == "" {
			t.Fatalf("hasil tidak lengkap: %+v", item)
		}
	}
	// The postings themselves are kept, because the result page needs the link.
	if len(store.jobs) != len(pending.Hasil) && len(store.jobs) != 2 {
		t.Fatalf("lowongan tersimpan = %d", len(store.jobs))
	}
}

func TestASecondRunOnTheSameCVReusesScoresAndSkipsTheModel(t *testing.T) {
	store := newFakeCariStore()
	src := &fakeSource{name: "uji", list: []jobs.Job{backendJob("1", "Backend Engineer (Go)")}}
	llm := &fakeLLM{responses: []string{judgementJSON(70, "Cocok: keahlian utama pelamar sesuai dengan permintaan.")}}
	svc := testCariService(store, llm, []jobs.Source{src}, CariConfig{PerIPLimit: 5})

	first, _ := svc.Search(context.Background(), "1.1.1.1", cariCV, "", false, 5)
	svc.process(context.Background(), &cariJob{runID: first.ID, cvText: cariCV,
		cvFingerprint: Fingerprint(cariCV), filters: filters{Keep: 5}})
	callsAfterFirst := len(llm.prompts)

	second, _ := svc.Search(context.Background(), "1.1.1.1", cariCV, "", false, 5)
	svc.process(context.Background(), &cariJob{runID: second.ID, cvText: cariCV,
		cvFingerprint: Fingerprint(cariCV), filters: filters{Keep: 5}})

	if len(llm.prompts) != callsAfterFirst {
		t.Fatalf("pencarian kedua masih memanggil model: %d -> %d panggilan", callsAfterFirst, len(llm.prompts))
	}
	got, _ := store.GetRun(context.Background(), second.ID)
	if len(got.Hasil) != 1 || got.Hasil[0].Skor != 70 {
		t.Fatalf("hasil dari cache tidak dipakai: %+v", got.Hasil)
	}
	if !strings.Contains(got.Pesan, "cache") {
		t.Fatalf("pesan tidak menyebut cache: %q", got.Pesan)
	}
}

func TestRemoteOnlyAndLocationFiltersAreHonoured(t *testing.T) {
	store := newFakeCariStore()
	remote := backendJob("r", "Backend Engineer (Go) Remote")
	remote.Remote = true
	remote.Location = "Remote"
	jakarta := backendJob("j", "Backend Engineer (Go)")
	jakarta.Location = "Jakarta, Indonesia"
	bandung := backendJob("b", "Backend Engineer (Go)")
	bandung.Location = "Bandung, Indonesia"
	src := &fakeSource{name: "uji", list: []jobs.Job{remote, jakarta, bandung}}
	llm := &fakeLLM{responses: []string{
		judgementJSON(80, "Cocok untuk posisi ini karena keahlian utamanya sesuai."),
		judgementJSON(80, "Cocok untuk posisi ini karena keahlian utamanya sesuai."),
		judgementJSON(80, "Cocok untuk posisi ini karena keahlian utamanya sesuai."),
	}}
	svc := testCariService(store, llm, []jobs.Source{src}, CariConfig{PerIPLimit: 5, Keep: 5})

	run, _ := svc.Search(context.Background(), "1.1.1.1", cariCV, "jakarta", false, 5)
	svc.process(context.Background(), &cariJob{runID: run.ID, cvText: cariCV,
		cvFingerprint: Fingerprint(cariCV), filters: filters{Location: "jakarta", Keep: 5}})
	got, _ := store.GetRun(context.Background(), run.ID)
	if len(got.Hasil) != 1 || !strings.Contains(got.Hasil[0].Job.Location, "Jakarta") {
		t.Fatalf("filter lokasi tidak jalan: %+v", got.Hasil)
	}

	run2, _ := svc.Search(context.Background(), "1.1.1.1", cariCV, "", true, 5)
	svc.process(context.Background(), &cariJob{runID: run2.ID, cvText: cariCV,
		cvFingerprint: Fingerprint(cariCV), filters: filters{RemoteOnly: true, Keep: 5}})
	got2, _ := store.GetRun(context.Background(), run2.ID)
	if len(got2.Hasil) != 1 || !got2.Hasil[0].Job.Remote {
		t.Fatalf("filter remote tidak jalan: %+v", got2.Hasil)
	}
}

func TestASourceThatFailsDoesNotKillTheRun(t *testing.T) {
	store := newFakeCariStore()
	good := &fakeSource{name: "bagus", list: []jobs.Job{backendJob("1", "Backend Engineer Go")}}
	bad := &fakeSource{name: "rusak", err: errors.New("403 from the board")}
	llm := &fakeLLM{responses: []string{judgementJSON(66, "Cukup cocok untuk lowongan ini berdasarkan keahlian yang ada.")}}
	svc := testCariService(store, llm, []jobs.Source{good, bad}, CariConfig{PerIPLimit: 5})

	run, _ := svc.Search(context.Background(), "1.1.1.1", cariCV, "", false, 5)
	svc.process(context.Background(), &cariJob{runID: run.ID, cvText: cariCV,
		cvFingerprint: Fingerprint(cariCV), filters: filters{Keep: 5}})
	got, _ := store.GetRun(context.Background(), run.ID)
	if got.Status != "selesai" || len(got.Hasil) != 1 {
		t.Fatalf("satu sumber rusak membatalkan pencarian: status=%q hasil=%d (%s)", got.Status, len(got.Hasil), got.Pesan)
	}
}

func TestNoCandidatesEndsHonestlyInsteadOfEmpty(t *testing.T) {
	store := newFakeCariStore()
	src := &fakeSource{name: "uji", list: []jobs.Job{
		{Source: "uji", ExternalID: "x", Title: "Barista Senior", Company: "Kafe",
			URL: "https://contoh.test/x", Description: "Menyeduh kopi dan melayani pelanggan."},
	}}
	svc := testCariService(store, &fakeLLM{}, []jobs.Source{src}, CariConfig{PerIPLimit: 5})
	run, _ := svc.Search(context.Background(), "1.1.1.1", cariCV, "", false, 5)
	svc.process(context.Background(), &cariJob{runID: run.ID, cvText: cariCV,
		cvFingerprint: Fingerprint(cariCV), filters: filters{Keep: 5}})
	got, _ := store.GetRun(context.Background(), run.ID)
	if got.Status != "selesai" {
		t.Fatalf("status = %q", got.Status)
	}
	if !strings.Contains(got.Pesan, "tidak ada lowongan") {
		t.Fatalf("pesan tidak jujur soal hasil kosong: %q", got.Pesan)
	}
}

func TestJudgementsThatDoNotFitTheContractAreRejected(t *testing.T) {
	cases := map[string]string{
		"skor di luar rentang":  `{"skor":150,"alasan":"` + strings.Repeat("a", 60) + `","celah":[]}`,
		"alasan terlalu pendek": `{"skor":50,"alasan":"ok","celah":[]}`,
		"bukan json":            `maaf, saya tidak bisa menilai`,
		"json terpotong":        `{"skor":50,"alasan":"potong`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			svc := testCariService(newFakeCariStore(), &fakeLLM{responses: []string{reply}}, nil, CariConfig{PerIPLimit: 5})
			if _, err := svc.judgeText(context.Background(), cariCV, backendJob("1", "Backend Engineer Go")); err == nil {
				t.Fatal("jawaban model yang tidak sesuai kontrak diterima")
			}
		})
	}
}

func TestJudgementAllowsZeroGaps(t *testing.T) {
	svc := testCariService(newFakeCariStore(), &fakeLLM{responses: []string{
		`{"skor":88,"alasan":"Cocok sekali: seluruh syarat utama terbukti di CV pelamar.","celah":[]}`,
	}}, nil, CariConfig{PerIPLimit: 5})
	j, err := svc.judgeText(context.Background(), cariCV, backendJob("1", "Backend Engineer Go"))
	if err != nil {
		t.Fatalf("celah kosong ditolak: %v", err)
	}
	if j.Skor != 88 || len(j.Celah) != 0 {
		t.Fatalf("hasil = %+v", j)
	}
}

func TestPromptCarriesThePostingAndTheConsistencyRule(t *testing.T) {
	llm := &fakeLLM{responses: []string{judgementJSON(60, "Cukup cocok untuk posisi ini berdasarkan pengalaman yang tercantum.")}}
	svc := testCariService(newFakeCariStore(), llm, nil, CariConfig{PerIPLimit: 5})
	if _, err := svc.judgeText(context.Background(), cariCV, backendJob("1", "Backend Engineer (Go)")); err != nil {
		t.Fatalf("judgeText: %v", err)
	}
	prompt := llm.prompts[0]
	for _, want := range []string{"Backend Engineer (Go)", "PT Uji", "Jakarta", cariCV, "bahasa Indonesia"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt tidak memuat %q", want)
		}
	}
	if !strings.Contains(prompt, "Jangan menjanjikan") {
		t.Fatal("prompt tidak melarang janji interview")
	}
}
