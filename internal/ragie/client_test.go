package ragie

import (
	"errors"
	"testing"
)

// A deployment without a RAGIE_API_KEY must degrade, not die: the retriever
// reports "not configured" and the pipeline marks the evaluation as running
// without rubric context.
func TestNoopClientReportsNotConfigured(t *testing.T) {
	var c NoopClient
	if _, err := c.RetrieveForCV("Backend Engineer"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("RetrieveForCV: got %v, want ErrNotConfigured", err)
	}
	if _, err := c.RetrieveForProject(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("RetrieveForProject: got %v, want ErrNotConfigured", err)
	}
}
