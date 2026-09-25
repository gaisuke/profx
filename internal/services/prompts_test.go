package services

import (
	"errors"
	"strings"
	"testing"
)

const rubricFixture = "RUBRIC: must have Go and Postgres; weight each 50%."

// The prompts are the product contract: the rubric must reach the model, the
// candidate content must reach the model, and the answer format must be
// unambiguous. These assertions describe that contract, not a frozen snapshot.
func TestBuildCVEvaluationPromptCarriesRubricAndFormat(t *testing.T) {
	p := buildCVEvaluationPrompt(rubricFixture, "CANDIDATE CV TEXT", "Backend Engineer")
	for _, want := range []string{
		"Backend Engineer",       // job title reaches the model
		rubricFixture,            // retrieved rubric reaches the model
		"CANDIDATE CV TEXT",      // the CV reaches the model
		"cv_match_rate",          // expected output field
		"cv_feedback",            // expected output field
		"Return ONLY valid JSON", // no prose wrappers to strip later
		"ONLY on the requirements and criteria from the CONTEXT", // anti-invention rule
		"0.8-0.9", // anchored score bands
	} {
		if !strings.Contains(p, want) {
			t.Errorf("CV prompt is missing %q", want)
		}
	}
}

func TestBuildProjectEvaluationPromptCarriesRubricAndFormat(t *testing.T) {
	p := buildProjectEvaluationPrompt(rubricFixture, "PROJECT REPORT TEXT")
	for _, want := range []string{
		rubricFixture,
		"PROJECT REPORT TEXT",
		"project_score",
		"project_feedback",
		"Return ONLY valid JSON",
		"1-5", // rubric scale must be stated
	} {
		if !strings.Contains(p, want) {
			t.Errorf("project prompt is missing %q", want)
		}
	}
}

func TestBuildFinalSummaryPromptFeedsBothStagesForward(t *testing.T) {
	p := buildFinalSummaryPrompt(
		&CVEvaluationResult{CVMatchRate: 0.82, CVFeedback: "strong Go background"},
		&ProjectEvaluationResult{ProjectScore: 4.3, ProjectFeedback: "clear design doc"},
	)
	for _, want := range []string{"0.82", "strong Go background", "4.3", "clear design doc", "overall_summary", "Return ONLY valid JSON"} {
		if !strings.Contains(p, want) {
			t.Errorf("summary prompt is missing %q", want)
		}
	}
}

// When retrieval is unavailable the prompt must tell the model what to do.
// Without this the model (correctly) refuses to invent criteria, answers with a
// zero score, and the job dies on validation with a message that explains
// nothing — which is exactly what happened on the first deployment.
func TestDegradedPromptTellsTheModelWhatToDo(t *testing.T) {
	degraded := degradedContext("CV", errors.New("retrieval matched no chunks"))
	cv := buildCVEvaluationPrompt(degraded, "CV TEXT", "Backend Engineer")
	if !strings.Contains(cv, "RUBRIC UNAVAILABLE") || !strings.Contains(cv, "Do NOT invent") {
		t.Error("degraded CV prompt must instruct the model not to invent criteria and to say so")
	}
	proj := buildProjectEvaluationPrompt(degraded, "REPORT TEXT")
	if !strings.Contains(proj, "RUBRIC UNAVAILABLE") {
		t.Error("degraded project prompt must say the rubric is missing")
	}
	if !strings.Contains(proj, "still return a score on the 1-5 scale") {
		t.Error("degraded project prompt must ask for a score on the rubric scale")
	}
}

func TestHealthyPromptCarriesNoDegradedNotice(t *testing.T) {
	cv := buildCVEvaluationPrompt(rubricFixture, "CV TEXT", "Backend Engineer")
	if strings.Contains(cv, "RUBRIC UNAVAILABLE") {
		t.Error("a prompt with a real rubric must not claim the rubric is missing")
	}
	degraded := degradedContext("CV", errors.New("retrieval matched no chunks"))
	if !rubricAvailable(rubricFixture) || rubricAvailable(degraded) {
		t.Error("rubricAvailable must distinguish real criteria from a degraded context")
	}
	if !strings.Contains(degraded, "matched no chunks") {
		t.Error("a degraded context must carry why retrieval failed")
	}
}
