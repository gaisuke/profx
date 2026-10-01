package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/gaisuke/profx/internal/services"
)

// clientIP reads the address the proxy saw. These headers are only trustworthy
// because the service binds loopback and nothing but nginx can reach it; exposed
// directly they would let a caller forge their own quota.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if first := strings.TrimSpace(strings.Split(v, ",")[0]); first != "" {
			return first
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// wantsMultipart reports whether the request carries a file upload. Everything
// else is expected to be JSON: accepting both silently is how an API ends up
// with two undocumented input formats.
func wantsMultipart(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "multipart/form-data")
}

// isJSON checks the content type for the endpoints that only take JSON.
func isJSON(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return true // an empty body with no type is handled by the decoder
	}
	return strings.HasPrefix(ct, "application/json")
}

// readCVFromRequest returns CV text from either a text field or an uploaded PDF.
// The PDF is parsed in memory and dropped: nothing about a CV is written to disk
// on this path.
func readCVFromRequest(r *http.Request, textField, fileField string, maxBytes int64) (string, error) {
	if err := r.ParseMultipartForm(maxBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return "", err
	}
	if text := strings.TrimSpace(r.FormValue(textField)); text != "" {
		return text, nil
	}
	file, header, err := r.FormFile(fileField)
	if err != nil {
		// Not an upload and not text: the caller sent neither, which the service
		// reports with a message that names the minimum length.
		return "", nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return "", err
	}
	name := ""
	if header != nil {
		name = header.Filename
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") && !looksLikePDF(data) {
		return "", errNotPDF
	}
	return services.ExtractPDFText(data)
}

var errNotPDF = errors.New("format berkas harus PDF")

func looksLikePDF(data []byte) bool {
	return len(data) > 4 && string(data[:4]) == "%PDF"
}

// setRateLimitHeaders publishes the allowance on the response, so a client can
// show a counter instead of discovering the limit by being refused.
func setRateLimitHeaders(w http.ResponseWriter, limit, remaining int, reset string) {
	if limit <= 0 {
		return
	}
	w.Header().Set(headerRateLimit, itoa(limit))
	w.Header().Set(headerRemaining, itoa(remaining))
	if reset != "" {
		w.Header().Set(headerReset, reset)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// startCleanup is where a longer-lived deployment would hang periodic work; the
// services already own their sweepers, so this exists only to keep the wiring in
// main.go honest about what it does not need to do.
var _ = context.Background
