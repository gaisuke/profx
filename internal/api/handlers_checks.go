package api

import (
	"net/http"
	"strings"
)

// timeLayout is the one timestamp format the contract promises.
const timeLayout = "2006-01-02T15:04:05Z07:00"

type createCheckRequest struct {
	JobTitle  string `json:"job_title"`
	JobDesc   string `json:"job_desc"`
	CVText    string `json:"cv_text"`
	Turnstile string `json:"turnstile_token"`
}

// createCheck is the free self-service check: one job description, one CV, a
// score and three gaps. Accepts JSON or a multipart upload, because a browser
// pastes text while a script tends to send a PDF.
func (s *server) createCheck(w http.ResponseWriter, r *http.Request) {
	var (
		jobTitle, jobDesc, cvText, token string
	)

	if wantsMultipart(r) {
		cv, err := readCVFromRequest(r, "cv_text", "cv_file", s.deps.MaxBody)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeValidation, err.Error())
			return
		}
		cvText = cv
		jobTitle = strings.TrimSpace(r.FormValue("job_title"))
		jobDesc = strings.TrimSpace(r.FormValue("job_desc"))
		token = r.FormValue("cf-turnstile-response")
	} else {
		if !isJSON(r) {
			writeError(w, http.StatusUnsupportedMediaType, codeUnsupported,
				"endpoint ini menerima application/json atau multipart/form-data.")
			return
		}
		var req createCheckRequest
		if !decodeJSON(w, r, &req, s.deps.MaxBody) {
			return
		}
		jobTitle = strings.TrimSpace(req.JobTitle)
		jobDesc = strings.TrimSpace(req.JobDesc)
		cvText = strings.TrimSpace(req.CVText)
		token = req.Turnstile
	}

	ip := clientIP(r)
	if s.deps.Turnstile != nil {
		if err := s.deps.Turnstile.Verify(r.Context(), ip, token); err != nil {
			writeValidationFailed(w, "turnstile_token", err.Error())
			return
		}
	}

	check, err := s.deps.Checks.Evaluate(r.Context(), ip, jobTitle, jobDesc, cvText)
	if err != nil {
		writeCheckError(w, err)
		return
	}

	if quota, qErr := s.deps.Checks.Quota(r.Context(), ip); qErr == nil {
		setRateLimitHeaders(w, quota.PerIPLimit, quota.Remaining, quota.ResetsAt)
	}
	writeJSON(w, http.StatusOK, newCheckResponse(check))
}

func (s *server) getCheck(w http.ResponseWriter, r *http.Request) {
	check, err := s.deps.Checks.Result(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newCheckResponse(check))
}

func (s *server) checkLimits(w http.ResponseWriter, r *http.Request) {
	quota, err := s.deps.Checks.Quota(r.Context(), clientIP(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "tidak bisa membaca kuota: "+err.Error())
		return
	}
	setRateLimitHeaders(w, quota.PerIPLimit, quota.Remaining, quota.ResetsAt)
	writeJSON(w, http.StatusOK, newCheckLimits(quota))
}

type interestRequest struct {
	Contact string `json:"contact"`
	Note    string `json:"note"`
}

// registerInterest records demand for the paid part before it exists. A waitlist
// is honest; a dead "buy now" button is not.
func (s *server) registerInterest(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, codeUnsupported,
			"endpoint ini menerima application/json.")
		return
	}
	var req interestRequest
	if !decodeJSON(w, r, &req, s.deps.MaxBody) {
		return
	}
	checkID := r.PathValue("id")
	if err := s.deps.Checks.Interest(r.Context(), checkID, req.Contact, req.Note); err != nil {
		if strings.Contains(err.Error(), "kontak") {
			writeValidationFailed(w, "contact", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "gagal menyimpan: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"check_id": checkID, "status": "recorded"})
}
