package utils

import (
	"bytes"
	"fmt"
	"math"

	"github.com/ledongthuc/pdf"
)

func ReadPDFContent(filepath string) (string, error) {
	f, r, err := pdf.Open(filepath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF file: %w", err)
	}
	defer f.Close()
	var buf bytes.Buffer
	b, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("failed to extract text from PDF: %w", err)
	}
	buf.ReadFrom(b)
	return buf.String(), nil
}

// NormalizeMatchRate puts the CV match rate on the 0-1 scale the schema and the
// prompt both assume: models occasionally answer in percent (85 instead of 0.85).
func NormalizeMatchRate(val float64) float64 {
	if math.IsNaN(val) {
		return 0
	}
	if val > 1 {
		val = val / 100
	}
	return clamp(val, 0, 1)
}

// NormalizeProjectScore keeps the rubric's own scale.
//
// The earlier shared NormalizeScore divided ANY value above 1 by 100, so a
// legitimate project score of 4.2 was stored as 0.042. That hack existed to
// satisfy a database CHECK constraint that only allowed 0.00-1.00 while the
// prompt and validator both used the rubric's 1-5 scale; migration 000003
// widens the constraint, so the stored number now means what the rubric says.
// This function only clamps absurd input and never rescales a valid value.
func NormalizeProjectScore(val float64) float64 {
	if math.IsNaN(val) {
		return 1
	}
	return clamp(val, 1, 5)
}

func clamp(val, lo, hi float64) float64 {
	if val < lo {
		return lo
	}
	if val > hi {
		return hi
	}
	return val
}
