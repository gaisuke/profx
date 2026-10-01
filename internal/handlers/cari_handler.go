package handlers

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gaisuke/profx/internal/services"
)

// maxCariBody bounds the CV upload for a search.
const maxCariBody = 8 << 20

type CariHandler struct {
	svc       *services.CariService
	turnstile *TurnstileVerifier
}

func NewCariHandler(svc *services.CariService, turnstile *TurnstileVerifier) *CariHandler {
	return &CariHandler{svc: svc, turnstile: turnstile}
}

func (h *CariHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/cari":
		h.search(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/cari/hasil/"):
		h.result(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/cari/info":
		h.info(w, r)
	default:
		writeCekJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "endpoint tidak dikenal"})
	}
}

func (h *CariHandler) search(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCariBody)
	if err := r.ParseMultipartForm(maxCariBody); err != nil {
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "kiriman tidak bisa dibaca: " + err.Error()})
		return
	}
	ip := clientIP(r)
	if err := h.turnstile.Verify(r.Context(), ip, r.FormValue("cf-turnstile-response")); err != nil {
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	cvText := strings.TrimSpace(r.FormValue("cv_text"))
	if cvText == "" {
		file, header, err := r.FormFile("cv_file")
		if err == nil {
			defer file.Close()
			data, err := io.ReadAll(io.LimitReader(file, maxCariBody))
			if err != nil {
				writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "berkas CV gagal dibaca"})
				return
			}
			name := ""
			if header != nil {
				name = header.Filename
			}
			if !strings.HasSuffix(strings.ToLower(name), ".pdf") && !looksLikePDF(data) {
				writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "format berkas harus PDF"})
				return
			}
			text, err := services.ExtractPDFText(data)
			if err != nil {
				writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			cvText = text
		}
	}

	keep, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("jumlah")))
	run, err := h.svc.Search(r.Context(), ip,
		cvText,
		strings.TrimSpace(r.FormValue("lokasi")),
		strings.EqualFold(strings.TrimSpace(r.FormValue("hanya_remote")), "true"),
		keep)
	switch {
	case errors.Is(err, services.ErrCariValidation):
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	case errors.Is(err, services.ErrCariQuota):
		writeCekJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": err.Error()})
		return
	case err != nil:
		log.Printf("[cari] gagal memulai: %v", err)
		writeCekJSON(w, http.StatusBadGateway, map[string]any{"ok": false,
			"error": "pencarian gagal dimulai. Kuota gratis kamu tidak terpakai — coba lagi sebentar lagi."})
		return
	}

	quota, qErr := h.svc.Quota(r.Context(), ip)
	resp := map[string]any{
		"ok":          true,
		"id":          run.ID,
		"status":      run.Status,
		"pesan":       run.Pesan,
		"kedaluwarsa": run.ExpiresAt.Format(time.RFC3339),
		"tautan":      "/profx/cari/hasil.html?id=" + run.ID,
	}
	if qErr == nil {
		resp["kuota"] = quota
	}
	writeCekJSON(w, http.StatusOK, resp)
}

func (h *CariHandler) result(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/cari/hasil/")
	if id == "" || strings.Contains(id, "/") {
		writeCekJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "pencarian tidak ditemukan"})
		return
	}
	run, err := h.svc.Result(r.Context(), id)
	if err != nil {
		writeCekJSON(w, http.StatusNotFound, map[string]any{"ok": false,
			"error": "pencarian ini sudah tidak ada (hasil gratis disimpan 48 jam)"})
		return
	}
	writeCekJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"id":          run.ID,
		"status":      run.Status,
		"pesan":       run.Pesan,
		"selesai":     run.Selesai,
		"total":       run.Total,
		"hasil":       run.Hasil,
		"dibuat":      run.CreatedAt.Format(time.RFC3339),
		"kedaluwarsa": run.ExpiresAt.Format(time.RFC3339),
	})
}

func (h *CariHandler) info(w http.ResponseWriter, r *http.Request) {
	quota, err := h.svc.Quota(r.Context(), clientIP(r))
	if err != nil {
		writeCekJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "tidak bisa membaca kuota"})
		return
	}
	writeCekJSON(w, http.StatusOK, map[string]any{"ok": true, "kuota": quota})
}
