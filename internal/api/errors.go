// Package api is the REST surface of profx: one JSON convention, one error
// shape, one place where routes are declared.
//
// The older handlers wrote three different response shapes depending on which
// endpoint you called — some wrapped everything in {"ok":true,...}, some returned
// a bare struct, and path parameters were cut out of the URL by hand. That is
// fine for pages this repository owns and hopeless for a frontend someone else
// writes, which is what this package is for.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gaisuke/profx/internal/services"
	"github.com/gaisuke/profx/internal/storage"
)

// errorCode is the machine-readable half of an error. Clients branch on this;
// humans read the message.
type errorCode string

const (
	codeValidation    errorCode = "validation_failed"
	codeNotFound      errorCode = "not_found"
	codeQuota         errorCode = "quota_exceeded"
	codeRateLimited   errorCode = "rate_limited"
	codeUnsupported   errorCode = "unsupported_media_type"
	codeTooLarge      errorCode = "payload_too_large"
	codeUpstream      errorCode = "upstream_failed"
	codeForbidden     errorCode = "forbidden"
	codeInternal      errorCode = "internal_error"
	codeMethodNotUsed errorCode = "method_not_allowed"
)

type errorDetail struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type errorBody struct {
	Code    errorCode     `json:"code"`
	Message string        `json:"message"`
	Details []errorDetail `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("[api] gagal menulis jawaban: %v", err)
	}
}

// writeError writes the one error shape the contract promises. Messages are
// Indonesian and safe to show a user as-is: they explain what to do, not what
// broke internally.
func writeError(w http.ResponseWriter, status int, code errorCode, message string, details ...errorDetail) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message, Details: details}})
}

// writeValidationFailed is the common case: one field, one reason.
func writeValidationFailed(w http.ResponseWriter, field, reason string) {
	writeError(w, http.StatusBadRequest, codeValidation, reason, errorDetail{Field: field, Reason: reason})
}

// writeCheckError maps the public-check service's error kinds to status codes.
// The distinction that matters to a visitor is whether they did something wrong
// (400, fixable), ran out of free checks (429, comes back tomorrow), or whether
// the model failed (502, and their quota was not spent).
func writeCheckError(w http.ResponseWriter, err error) {
	var ve *services.ValidationError
	switch {
	case errors.Is(err, services.ErrCekValidation):
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, codeValidation, err.Error(),
				errorDetail{Field: ve.Field, Reason: ve.Reason})
			return
		}
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, services.ErrCekQuota):
		writeError(w, http.StatusTooManyRequests, codeQuota, err.Error())
	case errors.Is(err, storage.ErrCheckNotFound):
		writeError(w, http.StatusNotFound, codeNotFound,
			"hasil cek ini sudah tidak ada. Hasil gratis disimpan 24 jam.")
	default:
		log.Printf("[api] cek gagal: %v", err)
		writeError(w, http.StatusBadGateway, codeUpstream,
			"penilaian gagal dijalankan (server penilai sedang lambat atau mati). Kuota cek gratis kamu tidak terpakai — coba lagi sebentar lagi.")
	}
}

// writeSearchError does the same for job searches.
func writeSearchError(w http.ResponseWriter, err error) {
	var ve *services.ValidationError
	switch {
	case errors.Is(err, services.ErrCariValidation):
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, codeValidation, err.Error(),
				errorDetail{Field: ve.Field, Reason: ve.Reason})
			return
		}
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, services.ErrCariQuota):
		writeError(w, http.StatusTooManyRequests, codeQuota, err.Error())
	case errors.Is(err, storage.ErrRunNotFound):
		writeError(w, http.StatusNotFound, codeNotFound,
			"pencarian ini sudah tidak ada. Hasil gratis disimpan 48 jam.")
	default:
		log.Printf("[api] pencarian gagal: %v", err)
		writeError(w, http.StatusBadGateway, codeUpstream,
			"pencarian gagal dijalankan. Kuota gratis kamu tidak terpakai — coba lagi sebentar lagi.")
	}
}

// decodeJSON reads a JSON body with a size limit, and says which of the two
// usual mistakes happened: too large, or not JSON at all.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeError(w, http.StatusRequestEntityTooLarge, codeTooLarge,
				"isi permintaan terlalu besar")
		default:
			writeError(w, http.StatusBadRequest, codeValidation,
				"badan permintaan bukan JSON yang bisa dibaca: "+err.Error())
		}
		return false
	}
	return true
}
