package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/storage"
)

// fakeStore keeps checks in memory and records what it was asked to save.
type fakeStore struct {
	mu       sync.Mutex
	checks   map[string]*models.PublicCheck
	ipHashes []string
	byIP     map[string]int
	total    int
	interest []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{checks: map[string]*models.PublicCheck{}, byIP: map[string]int{}}
}

func (f *fakeStore) Save(ctx context.Context, c *models.PublicCheck, ipHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks[c.ID] = c
	f.ipHashes = append(f.ipHashes, ipHash)
	f.byIP[ipHash]++
	f.total++
	return nil
}

func (f *fakeStore) GetByID(ctx context.Context, id string) (*models.PublicCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.checks[id]
	if !ok || c.ExpiresAt.Before(time.Now()) {
		return nil, storage.ErrCheckNotFound
	}
	return c, nil
}

func (f *fakeStore) CountByIPSince(ctx context.Context, ipHash string, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byIP[ipHash], nil
}

func (f *fakeStore) CountSince(ctx context.Context, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total, nil
}

func (f *fakeStore) SaveInterest(ctx context.Context, id, checkID, contact, note string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interest = append(f.interest, contact)
	return nil
}

func (f *fakeStore) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for id, c := range f.checks {
		if !c.ExpiresAt.After(now) {
			delete(f.checks, id)
			n++
		}
	}
	return n, nil
}

const (
	goodJobDesc = `Backend Engineer (Go) — Jakarta (hybrid).
Kami mencari engineer dengan pengalaman 3+ tahun Go, PostgreSQL, dan Kubernetes.
Tanggung jawab: merancang layanan berlatensi rendah, menjaga ketersediaan, menulis pengujian.
Syarat: paham HTTP dan API, pernah menangani trafik produksi, mengerti observability.`

	goodCV = `Budi Santoso — Backend Engineer
Pengalaman: 4 tahun membangun layanan Go untuk pembayaran. Menurunkan p99 dari 420ms ke 160ms.
Memimpin migrasi 12 layanan ke Kubernetes. Mengelola basis data PostgreSQL 180 juta baris.
Pendidikan: S1 Ilmu Komputer. Keahlian: Go, PostgreSQL, Kubernetes, gRPC, observability.`

	goodReply = `{
	  "skor": 68,
	  "ringkasan": "CV ini relevan dan bukti hasil kerjanya jelas, tetapi tidak menyebut pengujian otomatis dan observability secara eksplisit.",
	  "celah": [
	    {"bagian": "Pengalaman", "masalah": "Tidak menyebut pengujian otomatis sama sekali", "perbaikan": "Sebutkan jenis tes dan cakupannya, misalnya unit test untuk modul pembayaran."},
	    {"bagian": "Keahlian", "masalah": "Observability hanya tersirat lewat Kubernetes", "perbaikan": "Sebutkan alat yang dipakai, misalnya Prometheus atau OpenTelemetry."},
	    {"bagian": "Ringkasan", "masalah": "Ringkasan belum menyebut skala layanan yang dipegang", "perbaikan": "Tambahkan angka trafik harian yang kamu tangani."}
	  ],
	  "contoh_perbaikan": {
	    "sebelum": "Menurunkan p99 dari 420ms ke 160ms.",
	    "sesudah": "Menurunkan p99 layanan pembayaran 4k req/s dari 420ms ke 160ms lewat perbaikan query dan cache.",
	    "alasan": "Angka tanpa konteks skala mudah diabaikan perekrut."
	  }
	}`
)

// repeatReply gives the package's scripted fake one identical answer per call,
// which is what a multi-check test needs.
func repeatReply(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = goodReply
	}
	return out
}

func newCekService(store CekStore, llm LLM, cfg CekConfig) *CekService {
	if cfg.IPSalt == "" {
		cfg.IPSalt = "test-salt"
	}
	return NewCekService(store, llm, cfg)
}

func TestEvaluateStoresScoreButNeverTheCVOrJobDescription(t *testing.T) {
	store := newFakeStore()
	llm := &fakeLLM{responses: []string{goodReply}}
	svc := newCekService(store, llm, CekConfig{PerIPLimit: 3})

	check, err := svc.Evaluate(context.Background(), "203.0.113.7", "Backend Engineer", goodJobDesc, goodCV)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if check.Score != 68 {
		t.Fatalf("skor = %d, mau 68", check.Score)
	}
	if check.JobTitle != "Backend Engineer" {
		t.Fatalf("judul = %q", check.JobTitle)
	}

	// The retention promise: what is kept must not contain the CV body or the
	// job description body. Only the score, the findings, and the short label
	// the visitor typed survive.
	stored := fmt.Sprintf("%+v", *store.checks[check.ID])
	for _, secret := range []string{"Budi Santoso", "180 juta baris", "Tanggung jawab", "mengelola basis data"} {
		if strings.Contains(stored, secret) {
			t.Fatalf("hasil tersimpan memuat isi CV/lowongan (%q): %s", secret, stored)
		}
	}
	if !strings.Contains(stored, "Pengalaman") {
		t.Fatal("hasil tersimpan tidak memuat temuannya: hasil kosong bukan hasil yang aman")
	}
}

func TestEvaluateSendsCVAndJobDescToTheModel(t *testing.T) {
	llm := &fakeLLM{responses: []string{goodReply}}
	svc := newCekService(newFakeStore(), llm, CekConfig{PerIPLimit: 3})

	if _, err := svc.Evaluate(context.Background(), "1.2.3.4", "Backend Engineer", goodJobDesc, goodCV); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(llm.prompts) != 1 {
		t.Fatalf("panggilan model = %d, mau 1", len(llm.prompts))
	}
	prompt := llm.prompts[0]
	for _, want := range []string{goodJobDesc, "Budi Santoso", "bahasa Indonesia", "Jangan menjanjikan"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt tidak memuat %q", want)
		}
	}
}

func TestEvaluateRefusesShortInput(t *testing.T) {
	cases := []struct{ name, job, cv string }{
		{"lowongan terlalu pendek", "Backend", goodCV},
		{"cv terlalu pendek", goodJobDesc, "Nama: Budi"},
		{"keduanya kosong", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newCekService(newFakeStore(), &fakeLLM{responses: []string{goodReply}}, CekConfig{PerIPLimit: 3})
			_, err := svc.Evaluate(context.Background(), "1.2.3.4", "", tc.job, tc.cv)
			if !errors.Is(err, ErrCekValidation) {
				t.Fatalf("err = %v, mau ErrCekValidation", err)
			}
			// The message is shown to the visitor, so it must be readable and in
			// Indonesian, not a Go error string.
			if !strings.Contains(err.Error(), "minimal") {
				t.Fatalf("pesan tidak menjelaskan batasannya: %v", err)
			}
		})
	}
}

func TestPerVisitorQuotaStopsAtTheLimit(t *testing.T) {
	store := newFakeStore()
	svc := newCekService(store, &fakeLLM{responses: repeatReply(3)}, CekConfig{PerIPLimit: 2})

	for i := 0; i < 2; i++ {
		if _, err := svc.Evaluate(context.Background(), "9.9.9.9", "", goodJobDesc, goodCV); err != nil {
			t.Fatalf("cek ke-%d gagal: %v", i+1, err)
		}
	}
	_, err := svc.Evaluate(context.Background(), "9.9.9.9", "", goodJobDesc, goodCV)
	if !errors.Is(err, ErrCekQuota) {
		t.Fatalf("cek ke-3 err = %v, mau ErrCekQuota", err)
	}

	// A different visitor is unaffected: the quota is per person, not global.
	if _, err := svc.Evaluate(context.Background(), "8.8.8.8", "", goodJobDesc, goodCV); err != nil {
		t.Fatalf("pengunjung lain ditolak: %v", err)
	}

	q, err := svc.Quota(context.Background(), "9.9.9.9")
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if q.Remaining != 0 || q.PerIPUsed != 2 {
		t.Fatalf("kuota = %+v, mau used 2 remaining 0", q)
	}
}

func TestGlobalDailyCapAppliesAcrossVisitors(t *testing.T) {
	store := newFakeStore()
	svc := newCekService(store, &fakeLLM{responses: repeatReply(3)}, CekConfig{PerIPLimit: 10, GlobalLimit: 2})

	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		if _, err := svc.Evaluate(context.Background(), ip, "", goodJobDesc, goodCV); err != nil {
			t.Fatalf("cek dari %s gagal: %v", ip, err)
		}
	}
	_, err := svc.Evaluate(context.Background(), "3.3.3.3", "", goodJobDesc, goodCV)
	if !errors.Is(err, ErrCekQuota) {
		t.Fatalf("cek ke-3 err = %v, mau ErrCekQuota", err)
	}
	if !strings.Contains(err.Error(), "batas harian") {
		t.Fatalf("pesan tidak menyebut batas harian: %v", err)
	}
}

func TestIPIsHashedAndNeverStoredRaw(t *testing.T) {
	store := newFakeStore()
	svc := newCekService(store, &fakeLLM{responses: []string{goodReply}}, CekConfig{PerIPLimit: 3})

	if _, err := svc.Evaluate(context.Background(), "203.0.113.55", "", goodJobDesc, goodCV); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(store.ipHashes) != 1 {
		t.Fatalf("hash tersimpan = %d, mau 1", len(store.ipHashes))
	}
	if strings.Contains(store.ipHashes[0], "203.0.113.55") {
		t.Fatalf("alamat IP tersimpan mentah: %s", store.ipHashes[0])
	}
	if len(store.ipHashes[0]) != 64 {
		t.Fatalf("hash bukan sha256 hex: %q", store.ipHashes[0])
	}
	// Stable within a deployment, so the count survives a restart.
	if svc.HashIP("203.0.113.55") != store.ipHashes[0] {
		t.Fatal("hash tidak stabil")
	}
}

func TestResultRefusesSomethingExpired(t *testing.T) {
	store := newFakeStore()
	svc := newCekService(store, &fakeLLM{responses: []string{goodReply}}, CekConfig{PerIPLimit: 3, TTL: -time.Minute})
	check, err := svc.Evaluate(context.Background(), "1.1.1.1", "", goodJobDesc, goodCV)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if _, err := svc.Result(context.Background(), check.ID); !errors.Is(err, storage.ErrCheckNotFound) {
		t.Fatalf("err = %v, mau ErrCheckNotFound", err)
	}
	if n, err := svc.Cleanup(context.Background()); err != nil || n != 1 {
		t.Fatalf("Cleanup = %d, %v; mau 1, nil", n, err)
	}
}

func TestModelRepliesThatDoNotFitTheContractAreRejected(t *testing.T) {
	cases := map[string]string{
		"skor di luar rentang": `{"skor":180,"ringkasan":"` + strings.Repeat("a", 60) + `","celah":[{"bagian":"x","masalah":"12345678901","perbaikan":"12345678901"}],"contoh_perbaikan":{"sebelum":"12345678901","sesudah":"12345678901","alasan":"12345678901"}}`,
		"celah kurang dari 3":  `{"skor":50,"ringkasan":"` + strings.Repeat("a", 60) + `","celah":[{"bagian":"x","masalah":"12345678901","perbaikan":"12345678901"}],"contoh_perbaikan":{"sebelum":"12345678901","sesudah":"12345678901","alasan":"12345678901"}}`,
		"celah kosong":         `{"skor":50,"ringkasan":"` + strings.Repeat("a", 60) + `","celah":[{"bagian":"","masalah":"","perbaikan":""},{"bagian":"a","masalah":"","perbaikan":""},{"bagian":"b","masalah":"","perbaikan":""}],"contoh_perbaikan":{"sebelum":"12345678901","sesudah":"12345678901","alasan":"12345678901"}}`,
		"bukan json":           `maaf saya tidak bisa menilai CV ini`,
		"json terpotong":       `{"skor":50,"ringkasan":"potong`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			svc := newCekService(newFakeStore(), &fakeLLM{responses: []string{reply}}, CekConfig{PerIPLimit: 3})
			_, err := svc.Evaluate(context.Background(), "1.1.1.1", "", goodJobDesc, goodCV)
			if err == nil {
				t.Fatal("jawaban model yang tidak sesuai kontrak diterima")
			}
			if errors.Is(err, ErrCekQuota) || errors.Is(err, ErrCekValidation) {
				t.Fatalf("error salah kelas: %v", err)
			}
		})
	}
}

func TestInterestNeedsAReachableContact(t *testing.T) {
	store := newFakeStore()
	svc := newCekService(store, &fakeLLM{responses: []string{goodReply}}, CekConfig{PerIPLimit: 3})

	if err := svc.Interest(context.Background(), "abc", "  ", ""); !errors.Is(err, ErrCekValidation) {
		t.Fatalf("err = %v, mau ErrCekValidation", err)
	}
	if err := svc.Interest(context.Background(), "abc", "0812-3456-7890", "mau perbaikan CV"); err != nil {
		t.Fatalf("Interest: %v", err)
	}
	if len(store.interest) != 1 {
		t.Fatalf("minat tersimpan = %d, mau 1", len(store.interest))
	}
}

func TestDayStartIsMidnightJakarta(t *testing.T) {
	svc := newCekService(newFakeStore(), &fakeLLM{responses: []string{goodReply}}, CekConfig{PerIPLimit: 3})
	// 2026-01-02 01:30 WIB is still 2026-01-01 18:30 UTC.
	now := time.Date(2026, 1, 1, 18, 30, 0, 0, time.UTC)
	start := svc.DayStart(now)
	want := time.Date(2026, 1, 2, 0, 0, 0, 0, start.Location())
	if !start.Equal(want) {
		t.Fatalf("DayStart = %s, mau %s", start, want)
	}
}

func TestTitleFromJobDescUsesFirstRealLine(t *testing.T) {
	got := titleFromJobDesc("\n\n  Backend Engineer (Go) — Jakarta\nKami mencari...")
	if got != "Backend Engineer (Go) — Jakarta" {
		t.Fatalf("judul = %q", got)
	}
	long := titleFromJobDesc(strings.Repeat("x", 200))
	if len([]rune(long)) != 80 {
		t.Fatalf("judul panjang tidak dipotong: %d rune", len([]rune(long)))
	}
}

func TestExtractPDFTextRejectsGarbage(t *testing.T) {
	if _, err := ExtractPDFText([]byte("ini bukan pdf")); err == nil {
		t.Fatal("berkas bukan PDF diterima")
	}
	if _, err := ExtractPDFText(nil); err == nil {
		t.Fatal("berkas kosong diterima")
	}
}
