package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeLLM struct {
	responses []string
	errs      []error
	prompts   []string
}

func (f *fakeLLM) Generate(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	i := len(f.prompts) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return "", f.errs[i]
	}
	if i < len(f.responses) {
		return f.responses[i], nil
	}
	return "", fmt.Errorf("no scripted response for call %d", i)
}

type fakeRetriever struct {
	cvContext      string
	projectContext string
	err            error
	cvCalls        int
}

func (f *fakeRetriever) RetrieveForCV(string) (string, error) {
	f.cvCalls++
	return f.cvContext, f.err
}

func (f *fakeRetriever) RetrieveForProject() (string, error) { return f.projectContext, f.err }

const longFeedback = "The candidate demonstrates the required Go and Postgres depth, with gaps in Kubernetes operations."

func newTestService(model LLM, retriever Retriever) *EvaluationService {
	return &EvaluationService{llmClient: model, ragieClient: retriever}
}

func TestEvaluateCVTextCarriesRetrievedRubricIntoThePrompt(t *testing.T) {
	model := &fakeLLM{responses: []string{`{"cv_match_rate":0.83,"cv_feedback":"` + longFeedback + `"}`}}
	svc := newTestService(model, &fakeRetriever{cvContext: "RUBRIC: Go, Postgres, Kubernetes"})

	got, err := svc.evaluateCVText(context.Background(), "Backend Engineer", "CV: five years of Go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.CVMatchRate != 0.83 || got.CVFeedback != longFeedback {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(model.prompts) != 1 || !strings.Contains(model.prompts[0], "RUBRIC: Go, Postgres, Kubernetes") {
		t.Fatal("the retrieved rubric must reach the model prompt")
	}
	if !strings.Contains(model.prompts[0], "CV: five years of Go") {
		t.Fatal("the CV text must reach the model prompt")
	}
}

func TestEvaluateCVTextRejectsInvalidModelReplies(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		wantErr string
	}{
		{"score above scale", `{"cv_match_rate":1.5,"cv_feedback":"` + longFeedback + `"}`, "invalid CV result"},
		{"score below scale", `{"cv_match_rate":-0.2,"cv_feedback":"` + longFeedback + `"}`, "invalid CV result"},
		{"feedback too short", `{"cv_match_rate":0.5,"cv_feedback":"nope"}`, "invalid CV result"},
		{"not json", "I think this candidate is fine.", "failed to parse CV evaluation response"},
	}
	for _, tc := range cases {
		svc := newTestService(&fakeLLM{responses: []string{tc.reply}}, &fakeRetriever{cvContext: "rubric"})
		_, err := svc.evaluateCVText(context.Background(), "Backend Engineer", "CV")
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: expected error containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}

func TestEvaluateCVTextPropagatesModelFailure(t *testing.T) {
	svc := newTestService(&fakeLLM{errs: []error{errors.New("OpenCode Go returned 503: unavailable")}},
		&fakeRetriever{cvContext: "rubric"})
	_, err := svc.evaluateCVText(context.Background(), "Backend Engineer", "CV")
	if err == nil || !strings.Contains(err.Error(), "LLM call failed") {
		t.Fatalf("expected the model failure to surface, got %v", err)
	}
}

// When the rubric cannot be retrieved the pipeline keeps going (availability over
// strictness) — but it must retry first, and the prompt must say the rubric is
// missing rather than silently inventing one.
func TestEvaluateCVTextRetriesRetrievalAndMarksDegradedContext(t *testing.T) {
	retriever := &fakeRetriever{err: errors.New("ragie: 503")}
	model := &fakeLLM{responses: []string{`{"cv_match_rate":0.4,"cv_feedback":"` + longFeedback + `"}`}}
	svc := newTestService(model, retriever)

	if _, err := svc.evaluateCVText(context.Background(), "Backend Engineer", "CV"); err != nil {
		t.Fatalf("degraded retrieval must not fail the stage: %v", err)
	}
	if retriever.cvCalls != retrievalAttempts {
		t.Fatalf("retrieval attempts = %d, want %d", retriever.cvCalls, retrievalAttempts)
	}
	if !strings.Contains(model.prompts[0], fallbackContext) {
		t.Fatal("degraded prompt must state that no rubric context is available")
	}
}

func TestEvaluateProjectTextKeepsRubricScale(t *testing.T) {
	model := &fakeLLM{responses: []string{`{"project_score":4.2,"project_feedback":"` + longFeedback + `"}`}}
	svc := newTestService(model, &fakeRetriever{projectContext: "RUBRIC"})

	got, err := svc.evaluateProjectText(context.Background(), "case study")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ProjectScore != 4.2 {
		t.Fatalf("project score = %v, want 4.2 (the rubric scale must survive the pipeline)", got.ProjectScore)
	}
	svc2 := newTestService(&fakeLLM{responses: []string{`{"project_score":7.5,"project_feedback":"` + longFeedback + `"}`}},
		&fakeRetriever{projectContext: "RUBRIC"})
	if _, err := svc2.evaluateProjectText(context.Background(), "case study"); err == nil {
		t.Fatal("a score outside the rubric band must be rejected, not silently kept")
	}
}

func TestGenerateSummaryFeedsBothStagesForward(t *testing.T) {
	model := &fakeLLM{responses: []string{`{"overall_summary":"Strong hire: solid Go depth, needs Kubernetes exposure."}`}}
	svc := newTestService(model, &fakeRetriever{})

	summary, err := svc.generateSummary(context.Background(),
		&CVEvaluationResult{CVMatchRate: 0.83, CVFeedback: "strong Go background"},
		&ProjectEvaluationResult{ProjectScore: 4.2, ProjectFeedback: "clear design doc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(summary.OverallSummary, "Strong hire") {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	prompt := model.prompts[0]
	for _, want := range []string{"0.83", "4.2", "strong Go background", "clear design doc"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("summary prompt missing %q", want)
		}
	}
}
