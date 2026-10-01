package api

import (
	"net/http"
	"strings"
)

type createSearchRequest struct {
	CVText     string `json:"cv_text"`
	Location   string `json:"location"`
	RemoteOnly bool   `json:"remote_only"`
	MaxResults int    `json:"max_results"`
}

type createSearchResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

// createSearch starts a job search. It answers 202 with an id immediately,
// because judging a dozen postings takes tens of seconds and a client should not
// hold a connection open for it.
func (s *server) createSearch(w http.ResponseWriter, r *http.Request) {
	var (
		cvText     string
		location   string
		remoteOnly bool
		maxResults int
	)

	if wantsMultipart(r) {
		cv, err := readCVFromRequest(r, "cv_text", "cv_file", s.deps.MaxBody)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeValidation, err.Error())
			return
		}
		cvText = cv
		location = strings.TrimSpace(r.FormValue("location"))
		remoteOnly = strings.EqualFold(strings.TrimSpace(r.FormValue("remote_only")), "true")
		maxResults = atoiSafe(r.FormValue("max_results"))
	} else {
		if !isJSON(r) {
			writeError(w, http.StatusUnsupportedMediaType, codeUnsupported,
				"endpoint ini menerima application/json atau multipart/form-data.")
			return
		}
		var req createSearchRequest
		if !decodeJSON(w, r, &req, s.deps.MaxBody) {
			return
		}
		cvText = strings.TrimSpace(req.CVText)
		location = strings.TrimSpace(req.Location)
		remoteOnly = req.RemoteOnly
		maxResults = req.MaxResults
	}

	run, err := s.deps.Searches.Search(r.Context(), clientIP(r), cvText, location, remoteOnly, maxResults)
	if err != nil {
		writeSearchError(w, err)
		return
	}
	if quota, qErr := s.deps.Searches.Quota(r.Context(), clientIP(r)); qErr == nil {
		setRateLimitHeaders(w, quota.PerIPLimit, quota.Remaining, quota.ResetsAt)
	}
	writeJSON(w, http.StatusAccepted, createSearchResponse{
		ID:        run.ID,
		Status:    searchStatus(run.Status),
		Message:   run.Pesan,
		CreatedAt: run.CreatedAt.UTC().Format(timeLayout),
		ExpiresAt: run.ExpiresAt.UTC().Format(timeLayout),
	})
}

func (s *server) getSearch(w http.ResponseWriter, r *http.Request) {
	run, err := s.deps.Searches.Result(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSearchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newSearchResponse(run))
}

func (s *server) searchLimits(w http.ResponseWriter, r *http.Request) {
	quota, err := s.deps.Searches.Quota(r.Context(), clientIP(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "tidak bisa membaca kuota: "+err.Error())
		return
	}
	setRateLimitHeaders(w, quota.PerIPLimit, quota.Remaining, quota.ResetsAt)
	writeJSON(w, http.StatusOK, newSearchLimits(quota))
}

func atoiSafe(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 1000 {
			return 1000
		}
	}
	return n
}
