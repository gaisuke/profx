//go:build eval

// Evaluation harness: measures how well the model's scores agree with labelled
// expectations, and how much they move between runs.
//
//	go test -tags eval ./internal/services/ -run TestEvalHarness -v
//
// Environment:
//
//	EVAL_PROVIDER=opencodego|gemini   provider under test (default opencodego)
//	OPENCODE_GO_API_KEY               required for the default provider
//	GEMINI_API_KEY                    required when EVAL_PROVIDER=gemini
//	EVAL_STUB=1                       deterministic fake model: checks the harness, not the model
//	EVAL_REPEAT=3                     runs per case, for drift/spread measurement
//	EVAL_CASES / EVAL_OUT             override case and report directories
//
// Retrieval is replaced by the rubric embedded in each case file: the harness
// measures scoring quality with the rubric held constant, so run-to-run movement
// cannot come from retrieval variance.
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gaisuke/profx/internal/llm"
)

type evalCase struct {
	ID                 string  `json:"id"`
	JobTitle           string  `json:"job_title"`
	Rubric             string  `json:"rubric"`
	ProjectRubric      string  `json:"project_rubric"`
	CVText             string  `json:"cv_text"`
	ReportText         string  `json:"report_text"`
	ExpectedCVMin      float64 `json:"expected_cv_min"`
	ExpectedCVMax      float64 `json:"expected_cv_max"`
	ExpectedProjectMin float64 `json:"expected_project_min"`
	ExpectedProjectMax float64 `json:"expected_project_max"`
	Notes              string  `json:"notes"`
}

type evalRun struct {
	CaseID          string  `json:"case_id"`
	Repeat          int     `json:"repeat"`
	CVScore         float64 `json:"cv_match_rate"`
	CVInBand        bool    `json:"cv_in_band"`
	CVFeedback      string  `json:"cv_feedback"`
	ProjectScore    float64 `json:"project_score"`
	ProjectInBand   bool    `json:"project_in_band"`
	ProjectFeedback string  `json:"project_feedback"`
	Summary         string  `json:"overall_summary"`
	HardFailure     string  `json:"hard_failure,omitempty"`
	PromptHash      string  `json:"prompt_hash"`
	DurationMS      int64   `json:"duration_ms"`
	Notes           string  `json:"notes"`
}

type evalReport struct {
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Stub       bool      `json:"stub"`
	StartedAt  time.Time `json:"started_at"`
	Repeats    int       `json:"repeats"`
	CVInBand   string    `json:"cv_in_band"`
	CVMAE      float64   `json:"cv_mean_absolute_error"`
	ProjInBand string    `json:"project_in_band"`
	ProjMAE    float64   `json:"project_mean_absolute_error"`
	Failures   int       `json:"hard_failures"`
	MeanMS     int64     `json:"mean_latency_ms"`
	MaxSpread  float64   `json:"max_cv_spread"`
	Runs       []evalRun `json:"runs"`
}

// staticRetriever pins the rubric for a case instead of calling the RAG service.
type staticRetriever struct{ cv, project string }

func (s staticRetriever) RetrieveForCV(string) (string, error) { return s.cv, nil }
func (s staticRetriever) RetrieveForProject() (string, error)  { return s.project, nil }

// stubLLM lets the harness be exercised without a provider key: it answers the
// shape each stage expects with fixed values, so plumbing, scoring maths and the
// report can be verified. It says nothing about model quality.
type stubLLM struct{ calls int }

func (s *stubLLM) Generate(_ context.Context, prompt string) (string, error) {
	s.calls++
	switch {
	case strings.Contains(prompt, "cv_match_rate"):
		return `{"cv_match_rate":0.8,"cv_feedback":"stub: matches the rubric's must-have requirements with no measured gaps."}`, nil
	case strings.Contains(prompt, "project_score"):
		return `{"project_score":4.2,"project_feedback":"stub: strong reasoning with measured results and explicit trade-offs."}`, nil
	default:
		return `{"overall_summary":"stub: solid fit overall; strengths in depth, development area in breadth."}`, nil
	}
}

func TestEvalHarness(t *testing.T) {
	if os.Getenv("EVAL_ENABLE") != "1" {
		// The harness needs a real provider key; keep it out of ordinary runs.
		t.Skip("set EVAL_ENABLE=1 to run the evaluation harness")
	}
	casesDir := evalEnv("EVAL_CASES", filepath.Join("..", "..", "eval", "cases"))
	outDir := evalEnv("EVAL_OUT", filepath.Join("..", "..", "eval", "out"))
	repeats := evalEnvInt("EVAL_REPEAT", 1)
	stub := os.Getenv("EVAL_STUB") == "1"

	cases, err := loadEvalCases(casesDir)
	if err != nil {
		t.Fatalf("failed to load cases from %s: %v", casesDir, err)
	}
	if len(cases) == 0 {
		t.Fatalf("no cases found in %s", casesDir)
	}

	model, provider, modelName := evalModel(t, stub)
	report := evalReport{Provider: provider, Model: modelName, Stub: stub, StartedAt: time.Now(), Repeats: repeats}

	var cvErr, projErr []float64
	for _, tc := range cases {
		for repeat := 1; repeat <= repeats; repeat++ {
			run := runEvalCase(t, model, tc, repeat)
			report.Runs = append(report.Runs, run)
			if run.HardFailure != "" {
				report.Failures++
				continue
			}
			cvErr = append(cvErr, abs(run.CVScore-(tc.ExpectedCVMin+tc.ExpectedCVMax)/2))
			projErr = append(projErr, abs(run.ProjectScore-(tc.ExpectedProjectMin+tc.ExpectedProjectMax)/2))
		}
	}

	inBand, total := 0, 0
	for _, r := range report.Runs {
		if r.HardFailure == "" {
			total++
			if r.CVInBand {
				inBand++
			}
		}
	}
	report.CVInBand = fmt.Sprintf("%d/%d", inBand, total)
	pInBand, pTotal := 0, 0
	for _, r := range report.Runs {
		if r.HardFailure == "" {
			pTotal++
			if r.ProjectInBand {
				pInBand++
			}
		}
	}
	report.ProjInBand = fmt.Sprintf("%d/%d", pInBand, pTotal)
	report.CVMAE = mean(cvErr)
	report.ProjMAE = mean(projErr)
	report.MaxSpread = cvSpread(report.Runs)

	var totalMS int64
	for _, r := range report.Runs {
		totalMS += r.DurationMS
	}
	if len(report.Runs) > 0 {
		report.MeanMS = totalMS / int64(len(report.Runs))
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
	writeJSON(t, filepath.Join(outDir, "report.json"), report)
	writeMarkdown(t, filepath.Join(outDir, "report.md"), report, cases)

	t.Logf("provider=%s model=%s stub=%v", report.Provider, report.Model, report.Stub)
	t.Logf("CV in band: %s | CV MAE %.3f", report.CVInBand, report.CVMAE)
	t.Logf("project in band: %s | project MAE %.3f", report.ProjInBand, report.ProjMAE)
	t.Logf("hard failures: %d | mean latency %dms | max CV spread %.2f", report.Failures, report.MeanMS, report.MaxSpread)
	t.Logf("report written to %s", outDir)

	if report.Failures == len(report.Runs) {
		t.Fatal("every case failed: check the provider key, model name and network")
	}
}

func runEvalCase(t *testing.T, model LLM, tc evalCase, repeat int) evalRun {
	t.Helper()
	run := evalRun{CaseID: tc.ID, Repeat: repeat, Notes: tc.Notes}
	svc := newTestService(model, staticRetriever{cv: tc.Rubric, project: tc.ProjectRubric})
	ctx := context.Background()
	started := time.Now()

	cv, err := svc.evaluateCVText(ctx, tc.JobTitle, tc.CVText)
	if err != nil {
		run.HardFailure = "cv: " + err.Error()
		run.DurationMS = time.Since(started).Milliseconds()
		return run
	}
	run.CVScore = cv.CVMatchRate
	run.CVFeedback = cv.CVFeedback
	run.CVInBand = cv.CVMatchRate >= tc.ExpectedCVMin && cv.CVMatchRate <= tc.ExpectedCVMax
	run.PromptHash = hashPrompt(tc.Rubric + tc.CVText)

	proj, err := svc.evaluateProjectText(ctx, tc.ReportText)
	if err != nil {
		run.HardFailure = "project: " + err.Error()
		run.DurationMS = time.Since(started).Milliseconds()
		return run
	}
	run.ProjectScore = proj.ProjectScore
	run.ProjectFeedback = proj.ProjectFeedback
	run.ProjectInBand = proj.ProjectScore >= tc.ExpectedProjectMin && proj.ProjectScore <= tc.ExpectedProjectMax

	if summary, err := svc.generateSummary(ctx, cv, proj); err == nil {
		run.Summary = summary.OverallSummary
	} else {
		run.Summary = "summary stage failed: " + err.Error()
	}
	run.DurationMS = time.Since(started).Milliseconds()
	return run
}

func evalModel(t *testing.T, stub bool) (LLM, string, string) {
	t.Helper()
	if stub {
		return &stubLLM{}, "stub", "deterministic-fake"
	}
	provider := evalEnv("EVAL_PROVIDER", "opencodego")
	if provider == "gemini" {
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" {
			t.Skip("GEMINI_API_KEY not set")
		}
		modelName := evalEnv("GEMINI_MODEL", "gemini-1.5-flash")
		client, err := llm.NewGeminiClient(context.Background(), key, modelName)
		if err != nil {
			t.Fatalf("failed to init gemini client: %v", err)
		}
		return client, "gemini", modelName
	}
	key := os.Getenv("OPENCODE_GO_API_KEY")
	if key == "" {
		t.Skip("OPENCODE_GO_API_KEY not set")
	}
	client := llm.NewOpenCodeGoClient(key, os.Getenv("OPENCODE_GO_BASE_URL"), os.Getenv("OPENCODE_GO_MODEL"), 0)
	return client, "opencodego", client.Model()
}

func loadEvalCases(dir string) ([]evalCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var cases []evalCase
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var tc evalCase
		if err := json.Unmarshal(raw, &tc); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		cases = append(cases, tc)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal report: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func writeMarkdown(t *testing.T, path string, report evalReport, cases []evalCase) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# Evaluation harness report\n\n")
	fmt.Fprintf(&b, "- Provider: `%s` model `%s` (stub: %v)\n", report.Provider, report.Model, report.Stub)
	fmt.Fprintf(&b, "- Started: %s\n", report.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Cases: %d, repeats per case: %d\n", len(cases), report.Repeats)
	fmt.Fprintf(&b, "- Temperature: 0.2 (fixed in the client, so drift is not a decoding artefact)\n")
	fmt.Fprintf(&b, "- Rubric source: embedded in each case file (retrieval held constant)\n\n")
	fmt.Fprintf(&b, "## Scores\n\n")
	fmt.Fprintf(&b, "| case | CV expected | CV got | CV in band | project expected | project got | project in band |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
	for _, c := range cases {
		for _, r := range report.Runs {
			if r.CaseID != c.ID {
				continue
			}
			got := fmt.Sprintf("%.2f", r.CVScore)
			gotP := fmt.Sprintf("%.2f", r.ProjectScore)
			if r.HardFailure != "" {
				got, gotP = "FAILED", "FAILED"
			}
			fmt.Fprintf(&b, "| %s (run %d) | %.2f-%.2f | %s | %v | %.1f-%.1f | %s | %v |\n",
				c.ID, r.Repeat, c.ExpectedCVMin, c.ExpectedCVMax, got, r.CVInBand,
				c.ExpectedProjectMin, c.ExpectedProjectMax, gotP, r.ProjectInBand)
		}
	}
	fmt.Fprintf(&b, "\n## Summary\n\n")
	fmt.Fprintf(&b, "- CV match rate in band: **%s**\n", report.CVInBand)
	fmt.Fprintf(&b, "- CV mean absolute error vs band midpoint: %.3f\n", report.CVMAE)
	fmt.Fprintf(&b, "- Project score in band: **%s**\n", report.ProjInBand)
	fmt.Fprintf(&b, "- Project mean absolute error: %.3f\n", report.ProjMAE)
	fmt.Fprintf(&b, "- Hard failures (unparseable output, model error): %d\n", report.Failures)
	fmt.Fprintf(&b, "- Mean latency per case (3 stages): %dms\n", report.MeanMS)
	if report.Repeats > 1 {
		fmt.Fprintf(&b, "- Largest CV score spread between repeats: %.2f\n", report.MaxSpread)
	}
	fmt.Fprintf(&b, "\n## Summaries produced\n\n")
	for _, r := range report.Runs {
		fmt.Fprintf(&b, "- `%s` (run %d): %s\n", r.CaseID, r.Repeat, strings.TrimSpace(r.Summary))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func hashPrompt(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func cvSpread(runs []evalRun) float64 {
	perCase := map[string][]float64{}
	for _, r := range runs {
		if r.HardFailure == "" {
			perCase[r.CaseID] = append(perCase[r.CaseID], r.CVScore)
		}
	}
	maxSpread := 0.0
	for _, scores := range perCase {
		if len(scores) < 2 {
			continue
		}
		lo, hi := scores[0], scores[0]
		for _, s := range scores {
			if s < lo {
				lo = s
			}
			if s > hi {
				hi = s
			}
		}
		if hi-lo > maxSpread {
			maxSpread = hi - lo
		}
	}
	return maxSpread
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func evalEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func evalEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
