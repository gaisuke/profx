package utils

import (
	"math"
	"testing"
)

func TestNormalizeMatchRate(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"already on 0-1 scale", 0.85, 0.85},
		{"model answered in percent", 85, 0.85},
		{"zero is valid", 0, 0},
		{"perfect match", 1, 1},
		{"negative clamps to 0", -0.4, 0},
		{"absurd value clamps to 1", 850, 1},
		{"NaN becomes 0", math.NaN(), 0},
	}
	for _, tc := range cases {
		if got := NormalizeMatchRate(tc.in); got != tc.want {
			t.Errorf("%s: NormalizeMatchRate(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestNormalizeProjectScoreKeepsRubricScale is the regression test for the bug
// where every project score was divided by 100 (4.2 stored as 0.042).
func TestNormalizeProjectScoreKeepsRubricScale(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"rubric midpoint survives", 4.2, 4.2},
		{"top of scale survives", 5, 5},
		{"bottom of scale survives", 1, 1},
		{"below scale clamps to 1", 0.85, 1},
		{"above scale clamps to 5", 6.5, 5},
		{"NaN falls back to the floor", math.NaN(), 1},
	}
	for _, tc := range cases {
		if got := NormalizeProjectScore(tc.in); got != tc.want {
			t.Errorf("%s: NormalizeProjectScore(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestReadPDFContentRejectsNonPDF(t *testing.T) {
	if _, err := ReadPDFContent("testdata/does-not-exist.pdf"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
