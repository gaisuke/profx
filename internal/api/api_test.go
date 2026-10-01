package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gaisuke/profx/internal/models"
	"github.com/gaisuke/profx/internal/services"
	"github.com/gaisuke/profx/internal/storage"
)

// --- fakes -----------------------------------------------------------------

type fakeChecks struct {
	check       *models.PublicCheck
	evalErr     error
	quota       models.CekQuota
	interest    []string
	interestErr error
	lastIP      string
	lastDesc    string
}

func (f *fakeChecks) Evaluate(ctx context.Context, ip, jobTitle, jobDesc, cvText string) (*models.PublicCheck, error) {
	f.lastIP, f.lastDesc = ip, jobDesc
	if f.evalErr != nil {
		return nil, f.evalErr
	}
	// Mirror the real service's minimums, so a test of the 400 mapping is a test
	// of the mapping and not of the fake's politeness.
	if len(jobDesc) < services.MinJobDescChars {
		return nil, &services.ValidationError{Err: fmt.Errorf("%w: deskripsi lowongan minimal %d karakter supaya bisa dinilai dengan jujur",
			services.ErrCekValidation, services.MinJobDescChars), Field: "job_desc",
			Reason: fmt.Sprintf("deskripsi lowongan minimal %d karakter supaya bisa dinilai dengan jujur", services.MinJobDescChars)}
	}
	if len(cvText) < services.MinCVChars {
		return nil, &services.ValidationError{Err: fmt.Errorf("%w: isi CV minimal %d karakter (sekarang %d)",
			services.ErrCekValidation, services.MinCVChars, len(cvText)), Field: "cv_text",
			Reason: fmt.Sprintf("isi CV minimal %d karakter (sekarang %d)", services.MinCVChars, len(cvText))}
	}
	return f.check, nil
}

func (f *fakeChecks) Result(ctx context.Context, id string) (*models.PublicCheck, error) {
	if f.check == nil || f.check.ID != id {
		return nil, storage.ErrCheckNotFound
	}
	return f.check, nil
}

func (f *fakeChecks) Quota(ctx context.Context, ip string) (models.CekQuota, error) {
	return f.quota, nil
}

func (f *fakeChecks) Interest(ctx context.Context, checkID, contact, note string) error {
	if f.interestErr != nil {
		return f.interestErr
	}
	f.interest = append(f.interest, contact)
	return nil
}

type fakeSearches struct {
	run       *models.CariRun
	searchErr error
	quota     models.CariQuota
}

func (f *fakeSearches) Search(ctx context.Context, ip, cvText, location string, remoteOnly bool, maxResults int) (*models.CariRun, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.run, nil
}

func (f *fakeSearches) Result(ctx context.Context, id string) (*models.CariRun, error) {
	if f.run == nil || f.run.ID != id {
		return nil, storage.ErrRunNotFound
	}
	return f.run, nil
}

func (f *fakeSearches) Quota(ctx context.Context, ip string) (models.CariQuota, error) {
	return f.quota, nil
}

type fakeJobs struct {
	job    *models.EvaluationJob
	list   []models.EvaluationJob
	offset int
}

func (f *fakeJobs) CreateJob(jobTitle, cvDocID, reportDocID string) (*models.EvaluationJob, error) {
	return f.job, nil
}

func (f *fakeJobs) GetJobByID(id string) (*models.EvaluationJob, error) {
	if f.job == nil || f.job.ID != id {
		return nil, fmt.Errorf("job not found")
	}
	return f.job, nil
}

func (f *fakeJobs) ListRange(limit, offset int) ([]models.EvaluationJob, error) {
	f.offset = offset
	if offset > 0 {
		return nil, nil
	}
	return f.list, nil
}

type fakeUploads struct{ err error }

func (f *fakeUploads) UploadDocuments(cvFile, reportFile io.Reader, cvFilename, reportFilename string) (*models.UploadResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &models.UploadResponse{CandidateCVID: "cv-1", ProjectReportID: "rep-1"}, nil
}

type fakeDemo struct {
	enabled bool
	calls   int
}

func (f *fakeDemo) Enabled() bool { return f.enabled }
func (f *fakeDemo) Allow(ctx context.Context) (int, error) {
	f.calls++
	return 3, nil
}
func (f *fakeDemo) Status(ctx context.Context) (services.DemoStatus, error) {
	return services.DemoStatus{Enabled: f.enabled, Limit: 60, Used: 1, Remaining: 59, ResetsAt: time.Now().Add(12 * time.Hour).Format(time.RFC3339)}, nil
}

// --- helpers ---------------------------------------------------------------

func sampleCheck() *models.PublicCheck {
	now := time.Now()
	return &models.PublicCheck{
		ID: "chk123", JobTitle: "Backend Engineer (Go)", Score: 54,
		Payload: models.CekResult{
			Skor: 54, Ringkasan: "Relevan di inti backend, tapi beberapa syarat tidak terbukti di CV ini.",
			Celah: []models.CekGap{
				{Bagian: "Keahlian", Masalah: "Kubernetes tidak dibuktikan di produksi", Perbaikan: "Sebut peran konkret pada cluster produksi"},
				{Bagian: "Pengalaman", Masalah: "Observability hanya tersirat", Perbaikan: "Sebut alat yang dipakai"},
				{Bagian: "Ringkasan", Masalah: "Belum menyebut skala layanan", Perbaikan: "Tambahkan angka trafik harian"},
			},
			Contoh: models.ContohPerbaikan{Sebelum: "Mengembangkan layanan pembayaran dengan Go.", Sesudah: "Mengembangkan layanan pembayaran Go 4k req/s.", Alasan: "Angka membuat skala terlihat."},
		},
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
}

func testDeps() Deps {
	return Deps{
		Documents: &fakeUploads{},
		Jobs:      &fakeJobs{},
		Checks:    &fakeChecks{check: sampleCheck(), quota: models.CekQuota{PerIPLimit: 3, PerIPUsed: 1, Remaining: 2, GlobalLimit: 100, GlobalUsed: 4, ResetsAt: time.Now().Add(10 * time.Hour).Format(time.RFC3339)}},
		Searches:  &fakeSearches{run: &models.CariRun{ID: "src1", Status: "selesai", Pesan: "6 lowongan dinilai dari 324 yang diperiksa"}},
		Provider:  "opencodego",
		Sources:   []string{"kalibrr", "remotive"},
		Origins:   []string{"*"},
		MaxBody:   1 << 20,
		OpenAPI:   []byte("openapi: 3.0.3\n"),
	}
}

func do(t *testing.T, deps Deps, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("jawaban bukan JSON galat: %v (body: %s)", err, rec.Body.String())
	}
	return env.Error
}

// --- routing and shape -----------------------------------------------------

func TestUnknownPathAnswersTheDocumentedShape(t *testing.T) {
	rec := do(t, testDeps(), http.MethodGet, "/v1/tidak-ada", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, mau 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, mau JSON", ct)
	}
	if got := decodeEnvelope(t, rec); got.Code != codeNotFound {
		t.Fatalf("code = %q, mau %q", got.Code, codeNotFound)
	}
}

func TestWrongMethodAnswersJSONToo(t *testing.T) {
	// GET on a POST-only path: the mux would answer plain text on its own.
	rec := do(t, testDeps(), http.MethodGet, "/v1/checks", "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, mau 405", rec.Code)
	}
	if got := decodeEnvelope(t, rec); got.Code != codeMethodNotUsed {
		t.Fatalf("code = %q, mau %q", got.Code, codeMethodNotUsed)
	}
}

func TestEveryDocumentedRouteIsReachable(t *testing.T) {
	deps := testDeps()
	deps.Checks = &fakeChecks{check: sampleCheck()}
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/v1/health", http.StatusOK},
		{http.MethodGet, "/v1/openapi.yaml", http.StatusOK},
		{http.MethodGet, "/v1/checks/limits", http.StatusOK},
		{http.MethodGet, "/v1/checks/chk123", http.StatusOK},
		{http.MethodGet, "/v1/searches/limits", http.StatusOK},
		{http.MethodGet, "/v1/searches/src1", http.StatusOK},
		{http.MethodGet, "/v1/evaluations", http.StatusOK},
	}
	for _, c := range cases {
		rec := do(t, deps, c.method, c.path, "", nil)
		if rec.Code != c.want {
			t.Fatalf("%s %s → %d, mau %d (body: %s)", c.method, c.path, rec.Code, c.want, rec.Body.String())
		}
	}
}

func TestLimitsRouteIsNotSwallowedByTheIDRoute(t *testing.T) {
	// "limits" is a literal pattern and "{id}" a wildcard; the literal has to win,
	// otherwise /v1/checks/limits asks the store for a check called "limits".
	rec := do(t, testDeps(), http.MethodGet, "/v1/checks/limits", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bukan JSON: %v", err)
	}
	if _, ok := got["per_ip_per_day"]; !ok {
		t.Fatalf("jawaban bukan bentuk kuota: %s", rec.Body.String())
	}
}

// --- errors ---------------------------------------------------------------

func TestValidationErrorNamesTheField(t *testing.T) {
	rec := do(t, testDeps(), http.MethodPost, "/v1/checks", `{"job_desc":"terlalu pendek","cv_text":"`+strings.Repeat("a", 300)+`"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, mau 400", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != codeValidation {
		t.Fatalf("code = %q", got.Code)
	}
	if len(got.Details) == 0 || got.Details[0].Field != "job_desc" {
		t.Fatalf("detail tidak menyebut field: %+v", got.Details)
	}
	if !strings.Contains(got.Message, "minimal") {
		t.Fatalf("pesan tidak menjelaskan syaratnya: %q", got.Message)
	}
}

func TestQuotaExhaustedIs429WithHeaders(t *testing.T) {
	deps := testDeps()
	deps.Checks = &fakeChecks{evalErr: fmt.Errorf("%w: batas 3 cek gratis per orang per hari sudah terpakai", services.ErrCekQuota),
		quota: models.CekQuota{PerIPLimit: 3, Remaining: 0, ResetsAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}
	rec := do(t, deps, http.MethodPost, "/v1/checks", `{"job_desc":"`+strings.Repeat("a", 100)+`","cv_text":"`+strings.Repeat("b", 300)+`"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, mau 429", rec.Code)
	}
	if got := decodeEnvelope(t, rec); got.Code != codeQuota {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestModelFailureIsUpstreamAndSaysTheQuotaIsSafe(t *testing.T) {
	deps := testDeps()
	deps.Checks = &fakeChecks{evalErr: errors.New("panggilan model gagal: context deadline exceeded")}
	rec := do(t, deps, http.MethodPost, "/v1/checks", `{"job_desc":"`+strings.Repeat("a", 100)+`","cv_text":"`+strings.Repeat("b", 300)+`"}`, nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, mau 502", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != codeUpstream {
		t.Fatalf("code = %q", got.Code)
	}
	if !strings.Contains(got.Message, "tidak terpakai") {
		t.Fatalf("pesan tidak menenangkan soal kuota: %q", got.Message)
	}
}

func TestNotFoundForAnExpiredCheck(t *testing.T) {
	rec := do(t, testDeps(), http.MethodGet, "/v1/checks/tidakada", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decodeEnvelope(t, rec); got.Code != codeNotFound {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestNonJSONContentTypeIsRejected(t *testing.T) {
	rec := do(t, testDeps(), http.MethodPost, "/v1/evaluations", `job_title=x`,
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, mau 415", rec.Code)
	}
	if got := decodeEnvelope(t, rec); got.Code != codeUnsupported {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestUnknownJSONFieldIsRejectedRatherThanIgnored(t *testing.T) {
	// A typo in a client's payload must not be silently dropped: the caller would
	// get a 200 and no effect.
	rec := do(t, testDeps(), http.MethodPost, "/v1/evaluations",
		`{"job_title":"Backend","cv_document_id":"a","report_document_id":"b","job_tittle":"typo"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, mau 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// --- shapes ---------------------------------------------------------------

func TestCheckResponseUsesTheDocumentedEnglishKeys(t *testing.T) {
	rec := do(t, testDeps(), http.MethodPost, "/v1/checks",
		`{"job_title":"Backend Engineer","job_desc":"`+strings.Repeat("a", 100)+`","cv_text":"`+strings.Repeat("b", 300)+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID     string `json:"id"`
		Score  int    `json:"score"`
		Result struct {
			Summary string `json:"summary"`
			Gaps    []struct {
				Section string `json:"section"`
				Problem string `json:"problem"`
				Fix     string `json:"fix"`
			} `json:"gaps"`
			SampleFix struct {
				Before string `json:"before"`
				After  string `json:"after"`
				Reason string `json:"reason"`
			} `json:"sample_fix"`
		} `json:"result"`
		Links struct {
			Self string `json:"self"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bentuk jawaban tidak sesuai kontrak: %v\n%s", err, rec.Body.String())
	}
	if resp.ID != "chk123" || resp.Score != 54 || len(resp.Result.Gaps) != 3 {
		t.Fatalf("isi jawaban tidak lengkap: %+v", resp)
	}
	if resp.Result.SampleFix.After == "" || resp.Links.Self == "" {
		t.Fatalf("contoh perbaikan atau tautan hilang: %+v", resp)
	}
	// The Indonesian field names of the stored verdict must not leak out.
	if strings.Contains(rec.Body.String(), `"ringkasan"`) || strings.Contains(rec.Body.String(), `"contoh_perbaikan"`) {
		t.Fatalf("nama ruas internal bocor ke API: %s", rec.Body.String())
	}
}

func TestSearchLifecycle(t *testing.T) {
	deps := testDeps()
	running := &models.CariRun{ID: "src9", Status: "jalan", Pesan: "mencari lowongan yang cocok…",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(48 * time.Hour)}
	deps.Searches = &fakeSearches{run: running,
		quota: models.CariQuota{PerIPLimit: 2, Remaining: 1, ResetsAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}

	rec := do(t, deps, http.MethodPost, "/v1/searches",
		`{"cv_text":"`+strings.Repeat("c", 300)+`","location":"Indonesia","remote_only":false,"max_results":10}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, mau 202 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("bukan JSON: %v", err)
	}
	if created.ID != "src9" || created.Status != "running" {
		t.Fatalf("jawaban = %+v", created)
	}

	// Finished run with one result.
	deps.Searches = &fakeSearches{run: &models.CariRun{ID: "src9", Status: "selesai", Selesai: 1, Total: 1,
		Hasil: []models.MatchItem{{Skor: 52, Alasan: "Cocok di inti backend.", Celah: []string{"Java belum terbukti"},
			Job: models.JobSummary{Title: "Backend Developer", Company: "Trimegah", Location: "Jakarta", Source: "kalibrr", URL: "https://www.kalibrr.com/c/x/jobs/1/y"}}}}}
	rec = do(t, deps, http.MethodGet, "/v1/searches/src9", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Status   string `json:"status"`
		Progress struct {
			Scored int `json:"scored"`
			Total  int `json:"total"`
		} `json:"progress"`
		Results []struct {
			Score int `json:"score"`
			Job   struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"job"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bentuk jawaban salah: %v\n%s", err, rec.Body.String())
	}
	if got.Status != "done" || got.Progress.Scored != 1 || len(got.Results) != 1 || got.Results[0].Job.URL == "" {
		t.Fatalf("jawaban tidak lengkap: %+v", got)
	}
}

func TestSearchQuotaIs429(t *testing.T) {
	deps := testDeps()
	deps.Searches = &fakeSearches{searchErr: fmt.Errorf("%w: batas 2 pencarian gratis per orang per hari", services.ErrCariQuota)}
	rec := do(t, deps, http.MethodPost, "/v1/searches", `{"cv_text":"`+strings.Repeat("c", 300)+`"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, mau 429", rec.Code)
	}
	if got := decodeEnvelope(t, rec); got.Code != codeQuota {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestInterestRecordsAndValidatesTheContact(t *testing.T) {
	deps := testDeps()
	rec := do(t, deps, http.MethodPost, "/v1/checks/chk123/interest", `{"contact":"0812-3456-7890","note":"mau perbaikan"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "recorded") {
		t.Fatalf("jawaban = %s", rec.Body.String())
	}

	deps.Checks = &fakeChecks{interestErr: fmt.Errorf("%w: isi kontak (WhatsApp atau email) supaya bisa dikabari", services.ErrCekValidation)}
	rec = do(t, deps, http.MethodPost, "/v1/checks/chk123/interest", `{"contact":"x"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, mau 400", rec.Code)
	}
}

func TestEvaluationListIsForbiddenInDemoMode(t *testing.T) {
	deps := testDeps()
	demo := &fakeDemo{enabled: true}
	deps.Demo = demo
	rec := do(t, deps, http.MethodGet, "/v1/evaluations", "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, mau 403", rec.Code)
	}
	if got := decodeEnvelope(t, rec); got.Code != codeForbidden {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestEvaluationResponseKeepsNullsForUnfinishedNumbers(t *testing.T) {
	deps := testDeps()
	deps.Jobs = &fakeJobs{job: &models.EvaluationJob{ID: "ev1", JobTitle: "Backend", Status: models.JobStatusQueued, CreatedAt: time.Now()}}
	rec := do(t, deps, http.MethodGet, "/v1/evaluations/ev1", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, key := range []string{`"cv_match_rate":null`, `"project_score":null`, `"completed_at":null`} {
		if !strings.Contains(body, key) {
			t.Fatalf("ruas %s tidak muncul sebagai null: %s", key, body)
		}
	}
}

func TestCompletedEvaluationCarriesItsNumbers(t *testing.T) {
	deps := testDeps()
	rate, score := 0.83, 4.2
	feedback := "Kandidat kuat di Go dan Postgres."
	deps.Jobs = &fakeJobs{job: &models.EvaluationJob{
		ID: "ev2", JobTitle: "Backend Engineer", Status: models.JobStatusCompleted, CreatedAt: time.Now(),
		CVMatchRate:  sql.NullFloat64{Float64: rate, Valid: true},
		CVFeedback:   sql.NullString{String: feedback, Valid: true},
		ProjectScore: sql.NullFloat64{Float64: score, Valid: true},
		CompletedAt:  sql.NullTime{Time: time.Now(), Valid: true},
	}}
	rec := do(t, deps, http.MethodGet, "/v1/evaluations/ev2", "", nil)
	var got evaluationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bukan JSON: %v", err)
	}
	if got.CVMatchRate == nil || *got.CVMatchRate != rate || got.ProjectScore == nil || *got.ProjectScore != score {
		t.Fatalf("angka tidak terbawa: %+v", got)
	}
	if got.CompletedAt == nil {
		t.Fatal("completed_at kosong padahal selesai")
	}
}

func TestListEvaluationsPaginates(t *testing.T) {
	deps := testDeps()
	jobs := &fakeJobs{list: []models.EvaluationJob{{ID: "a", JobTitle: "A", Status: models.JobStatusCompleted, CreatedAt: time.Now()}}}
	deps.Jobs = jobs
	rec := do(t, deps, http.MethodGet, "/v1/evaluations?limit=5&offset=0", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Data []evaluationResponse `json:"data"`
		Meta struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
			Count  int `json:"count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bukan JSON: %v", err)
	}
	if got.Meta.Limit != 5 || got.Meta.Count != 1 || len(got.Data) != 1 {
		t.Fatalf("meta salah: %+v", got.Meta)
	}

	rec = do(t, deps, http.MethodGet, "/v1/evaluations?limit=abc", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("limit ngawur diterima: %d", rec.Code)
	}
}

// --- middleware ------------------------------------------------------------

func TestCORSPreflightAndOriginEcho(t *testing.T) {
	deps := testDeps()
	deps.Origins = []string{"https://app.teman.dev"}

	rec := do(t, deps, http.MethodOptions, "/v1/checks", "", map[string]string{"Origin": "https://app.teman.dev"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, mau 204", rec.Code)
	}
	if got := rec.Header().Get(headerAllowOrig); got != "https://app.teman.dev" {
		t.Fatalf("allow-origin = %q", got)
	}
	if !strings.Contains(rec.Header().Get(headerAllowMeth), "POST") {
		t.Fatalf("allow-methods = %q", rec.Header().Get(headerAllowMeth))
	}

	// An origin that is not on the list gets no permission header at all.
	rec = do(t, deps, http.MethodGet, "/v1/health", "", map[string]string{"Origin": "https://jahat.example"})
	if got := rec.Header().Get(headerAllowOrig); got != "" {
		t.Fatalf("origin asing diizinkan: %q", got)
	}
}

func TestPreflightAlsoCarriesTheRequestID(t *testing.T) {
	// The contract says every response carries X-Request-Id, and the preflight is
	// a response: it used to be the exception because cors answered it before the
	// identifier middleware ran.
	rec := do(t, testDeps(), http.MethodOptions, "/v1/checks", "",
		map[string]string{"Origin": "https://app.teman.dev", "Access-Control-Request-Method": "POST"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, mau 204", rec.Code)
	}
	if rec.Header().Get(headerRequestID) == "" {
		t.Fatal("preflight tidak membawa X-Request-Id")
	}
}

func TestQuotaResetBodyAndHeaderDescribeOneInstant(t *testing.T) {
	deps := testDeps()
	deps.Checks = &fakeChecks{quota: models.CekQuota{
		PerIPLimit: 3, PerIPUsed: 1, Remaining: 2,
		ResetsAt: "2026-10-01T17:00:00Z", // midnight Jakarta, written in UTC
	}}
	rec := do(t, deps, http.MethodGet, "/v1/checks/limits", "", nil)
	var got struct {
		ResetsAt string `json:"resets_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bukan JSON: %v", err)
	}
	body, err := time.Parse(time.RFC3339, got.ResetsAt)
	if err != nil {
		t.Fatalf("resets_at tidak RFC3339: %q", got.ResetsAt)
	}
	header, err := time.Parse(time.RFC3339, rec.Header().Get(headerReset))
	if err != nil {
		t.Fatalf("header reset tidak RFC3339: %q", rec.Header().Get(headerReset))
	}
	// One instant, described once: the value used to be converted to UTC in the
	// body while the header kept the offset it came with.
	if !body.Equal(header) {
		t.Fatalf("body %s dan header %s bukan waktu yang sama", body, header)
	}
	if !strings.Contains(got.ResetsAt, "+07:00") {
		t.Fatalf("resets_at = %q, mau waktu Jakarta (+07:00)", got.ResetsAt)
	}
}

func TestCORSAllowsAnyOriginByDefault(t *testing.T) {
	rec := do(t, testDeps(), http.MethodGet, "/v1/health", "", map[string]string{"Origin": "https://apa-saja.example"})
	if got := rec.Header().Get(headerAllowOrig); got != "*" {
		t.Fatalf("allow-origin = %q, mau *", got)
	}
}

func TestRequestIDIsEchoedOrGenerated(t *testing.T) {
	rec := do(t, testDeps(), http.MethodGet, "/v1/health", "", map[string]string{headerRequestID: "uji-123"})
	if got := rec.Header().Get(headerRequestID); got != "uji-123" {
		t.Fatalf("request id = %q, mau diteruskan", got)
	}
	rec = do(t, testDeps(), http.MethodGet, "/v1/health", "", map[string]string{headerRequestID: "id yang tidak aman!"})
	if got := rec.Header().Get(headerRequestID); got == "id yang tidak aman!" || got == "" {
		t.Fatalf("id tidak aman diteruskan apa adanya: %q", got)
	}
}

func TestRateLimitHeadersArePublished(t *testing.T) {
	rec := do(t, testDeps(), http.MethodGet, "/v1/checks/limits", "", nil)
	if rec.Header().Get(headerRateLimit) != "3" || rec.Header().Get(headerRemaining) != "2" {
		t.Fatalf("header kuota = %q/%q", rec.Header().Get(headerRateLimit), rec.Header().Get(headerRemaining))
	}
	if rec.Header().Get(headerReset) == "" {
		t.Fatal("header reset kuota kosong")
	}
}

func TestHealthReportsWiring(t *testing.T) {
	rec := do(t, testDeps(), http.MethodGet, "/v1/health", "", nil)
	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bukan JSON: %v", err)
	}
	if got.Status != "ok" || got.Provider != "opencodego" || len(got.Sources) != 2 {
		t.Fatalf("health = %+v", got)
	}
	if got.Quotas.Checks.PerIPPerDay != 3 {
		t.Fatalf("kuota cek tidak dilaporkan: %+v", got.Quotas.Checks)
	}
}

func TestOpenAPISpecIsServedAsYAML(t *testing.T) {
	rec := do(t, testDeps(), http.MethodGet, "/v1/openapi.yaml", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "openapi") {
		t.Fatalf("isi bukan spesifikasi: %s", rec.Body.String())
	}
}

func TestUploadRequiresMultipart(t *testing.T) {
	rec := do(t, testDeps(), http.MethodPost, "/v1/documents", `{"a":1}`, nil)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, mau 415", rec.Code)
	}
}

func TestUploadReportsAMissingFileByField(t *testing.T) {
	deps := testDeps()
	body := &strings.Builder{}
	body.WriteString("--batas\r\nContent-Disposition: form-data; name=\"candidate_cv\"; filename=\"cv.pdf\"\r\nContent-Type: application/pdf\r\n\r\n%PDF-1.4 isi\r\n--batas--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=batas")
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeEnvelope(t, rec); len(got.Details) == 0 || got.Details[0].Field != "project_report" {
		t.Fatalf("field yang hilang tidak disebut: %+v", got)
	}
}
