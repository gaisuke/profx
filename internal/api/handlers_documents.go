package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gaisuke/profx/internal/services"
)

const (
	defaultEvaluationLimit = 20
	maxEvaluationLimit     = 100
)

// uploadDocuments stores the two PDFs an evaluation needs. It is the only
// endpoint that writes files, and the only one where the response is a pair of
// ids rather than a result.
func (s *server) uploadDocuments(w http.ResponseWriter, r *http.Request) {
	if !wantsMultipart(r) {
		writeError(w, http.StatusUnsupportedMediaType, codeUnsupported,
			"endpoint ini menerima multipart/form-data dengan dua berkas PDF: candidate_cv dan project_report.")
		return
	}
	if err := r.ParseMultipartForm(s.deps.MaxBody); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "kiriman tidak bisa dibaca: "+err.Error())
		return
	}

	cvFile, cvHeader, err := r.FormFile("candidate_cv")
	if err != nil {
		writeValidationFailed(w, "candidate_cv", "berkas candidate_cv (PDF) wajib diunggah")
		return
	}
	defer cvFile.Close()

	reportFile, reportHeader, err := r.FormFile("project_report")
	if err != nil {
		writeValidationFailed(w, "project_report", "berkas project_report (PDF) wajib diunggah")
		return
	}
	defer reportFile.Close()

	resp, err := s.deps.Documents.UploadDocuments(cvFile, reportFile, cvHeader.Filename, reportHeader.Filename)
	switch {
	case errors.Is(err, services.ErrInvalidFileType):
		writeError(w, http.StatusBadRequest, codeValidation,
			"kedua berkas harus PDF",
			errorDetail{Field: "candidate_cv", Reason: "harus PDF"},
			errorDetail{Field: "project_report", Reason: "harus PDF"})
	case err != nil:
		writeError(w, http.StatusInternalServerError, codeInternal, "berkas gagal disimpan: "+err.Error())
	default:
		writeJSON(w, http.StatusCreated, documentsResponse{
			CandidateCVID:   resp.CandidateCVID,
			ProjectReportID: resp.ProjectReportID,
		})
	}
}

type createEvaluationRequest struct {
	JobTitle         string `json:"job_title"`
	CVDocumentID     string `json:"cv_document_id"`
	ReportDocumentID string `json:"report_document_id"`
}

type createEvaluationResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	JobTitle  string `json:"job_title"`
	CreatedAt string `json:"created_at"`
}

func (s *server) createEvaluation(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, codeUnsupported,
			"endpoint ini menerima application/json.")
		return
	}
	var req createEvaluationRequest
	if !decodeJSON(w, r, &req, s.deps.MaxBody) {
		return
	}
	switch {
	case strings.TrimSpace(req.JobTitle) == "":
		writeValidationFailed(w, "job_title", "job_title wajib diisi")
		return
	case strings.TrimSpace(req.CVDocumentID) == "":
		writeValidationFailed(w, "cv_document_id", "cv_document_id wajib diisi (dari POST /v1/documents)")
		return
	case strings.TrimSpace(req.ReportDocumentID) == "":
		writeValidationFailed(w, "report_document_id", "report_document_id wajib diisi (dari POST /v1/documents)")
		return
	}

	if s.deps.Demo != nil && s.deps.Demo.Enabled() {
		remaining, err := s.deps.Demo.Allow(r.Context())
		switch {
		case errors.Is(err, services.ErrDemoQuotaExceeded):
			w.Header().Set("Retry-After", "3600")
			writeError(w, http.StatusTooManyRequests, codeQuota,
				"jatah evaluasi demo hari ini sudah habis. "+err.Error())
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, codeInternal,
				"tidak bisa memeriksa kuota demo: "+err.Error())
			return
		default:
			status, _ := s.deps.Demo.Status(r.Context())
			setRateLimitHeaders(w, status.Limit, remaining, status.ResetsAt)
		}
	}

	job, err := s.deps.Jobs.CreateJob(req.JobTitle, req.CVDocumentID, req.ReportDocumentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal,
			"gagal membuat pekerjaan evaluasi: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, createEvaluationResponse{
		ID:        job.ID,
		Status:    string(job.Status),
		JobTitle:  job.JobTitle,
		CreatedAt: job.CreatedAt.UTC().Format(timeLayout),
	})
}

func (s *server) getEvaluation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeValidationFailed(w, "id", "id evaluasi wajib diisi")
		return
	}
	job, err := s.deps.Jobs.GetJobByID(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, codeNotFound, "evaluasi dengan id itu tidak ditemukan")
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, "gagal mengambil evaluasi: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newEvaluationResponse(job))
}

func (s *server) listEvaluations(w http.ResponseWriter, r *http.Request) {
	// A shared deployment must not let one caller list another's evaluations.
	// Individual results stay reachable by their unguessable id, which the caller
	// already holds.
	if s.deps.Demo != nil && s.deps.Demo.Enabled() {
		writeError(w, http.StatusForbidden, codeForbidden,
			"riwayat evaluasi dimatikan pada deployment publik. Hasilmu sendiri tetap bisa dibuka lewat tautannya.")
		return
	}

	limit := defaultEvaluationLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeValidationFailed(w, "limit", "limit harus bilangan bulat positif")
			return
		}
		if parsed > maxEvaluationLimit {
			parsed = maxEvaluationLimit
		}
		limit = parsed
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeValidationFailed(w, "offset", "offset harus bilangan bulat >= 0")
			return
		}
		offset = parsed
	}

	jobs, err := s.deps.Jobs.ListRange(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "gagal membaca daftar evaluasi: "+err.Error())
		return
	}
	data := make([]evaluationResponse, 0, len(jobs))
	for i := range jobs {
		data = append(data, newEvaluationResponse(&jobs[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": limit, "offset": offset, "count": len(data)},
	})
}
