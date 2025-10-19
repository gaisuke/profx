package utils

import (
	"bytes"
	"fmt"

	"github.com/ledongthuc/pdf"
)

func ReadPDFContent(filepath string) (string, error) {
	pdf.DebugOn = true

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
