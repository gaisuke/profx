package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gaisuke/profx/internal/services"
)

// maxCekBody bounds an upload: a CV PDF plus a pasted job description. Eight
// megabytes is generous for a text CV and small enough that a hostile client
// cannot buffer much.
const maxCekBody = 8 << 20

// turnstileVerifyURL is Cloudflare's siteverify endpoint. The verifier exists
// because /cek is the one unauthenticated endpoint that costs model money; it
// stays disabled until a site key is configured, and says so at startup.
const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type CekHandler struct {
	svc       *services.CekService
	turnstile *TurnstileVerifier
}

func NewCekHandler(svc *services.CekService, turnstile *TurnstileVerifier) *CekHandler {
	return &CekHandler{svc: svc, turnstile: turnstile}
}

// TurnstileVerifier checks a Cloudflare Turnstile token. A nil verifier means
// "not configured": the check is skipped and the deployment relies on the
// per-visitor quota and the proxy's rate limiting instead.
type TurnstileVerifier struct {
	Secret string
	client *http.Client
	url    string
}

func NewTurnstileVerifier(secret string) *TurnstileVerifier {
	if strings.TrimSpace(secret) == "" {
		return nil
	}
	return &TurnstileVerifier{
		Secret: secret,
		client: &http.Client{Timeout: 10 * time.Second},
		url:    turnstileVerifyURL,
	}
}

func (t *TurnstileVerifier) Verify(ctx context.Context, ip, token string) error {
	if t == nil {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("verifikasi manusia belum selesai, muat ulang halaman lalu coba lagi")
	}
	form := url.Values{}
	form.Set("secret", t.Secret)
	form.Set("response", token)
	if ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("tidak bisa menghubungi layanan verifikasi: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("jawaban verifikasi tidak bisa dibaca")
	}
	if !out.Success {
		return fmt.Errorf("verifikasi manusia gagal, coba lagi")
	}
	return nil
}

func (h *CekHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/cek":
		h.create(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/cek/hasil/"):
		h.result(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/cek/info":
		h.info(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/cek/minat":
		h.interest(w, r)
	default:
		writeCekJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "endpoint tidak dikenal"})
	}
}

// create is the public check: job description + CV in, score and findings out.
func (h *CekHandler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCekBody)
	if err := r.ParseMultipartForm(maxCekBody); err != nil {
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "kiriman tidak bisa dibaca: " + err.Error()})
		return
	}

	ip := clientIP(r)
	jobTitle := strings.TrimSpace(r.FormValue("job_title"))
	jobDesc := strings.TrimSpace(r.FormValue("job_desc"))
	cvText := strings.TrimSpace(r.FormValue("cv_text"))

	if err := h.turnstile.Verify(r.Context(), ip, r.FormValue("cf-turnstile-response")); err != nil {
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// A PDF is read and dropped: nothing about the file is written to disk.
	if cvText == "" {
		file, header, err := r.FormFile("cv_file")
		if err == nil {
			defer file.Close()
			data, err := io.ReadAll(io.LimitReader(file, maxCekBody))
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

	check, err := h.svc.Evaluate(r.Context(), ip, jobTitle, jobDesc, cvText)
	switch {
	case errors.Is(err, services.ErrCekValidation):
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	case errors.Is(err, services.ErrCekQuota):
		writeCekJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": err.Error()})
		return
	case err != nil:
		log.Printf("[cek] gagal: %v", err)
		writeCekJSON(w, http.StatusBadGateway, map[string]any{"ok": false,
			"error": "penilaian gagal dijalankan (server penilai sedang lambat atau mati). Kuota cek gratis kamu tidak terpakai — coba lagi sebentar lagi."})
		return
	}

	quota, qErr := h.svc.Quota(r.Context(), ip)
	resp := map[string]any{
		"ok":          true,
		"id":          check.ID,
		"job_title":   check.JobTitle,
		"skor":        check.Score,
		"hasil":       check.Payload,
		"kedaluwarsa": check.ExpiresAt.Format(time.RFC3339),
		"tautan":      "/profx/cek/hasil.html?id=" + check.ID,
	}
	if qErr == nil {
		resp["kuota"] = quota
	}
	writeCekJSON(w, http.StatusOK, resp)
}

func (h *CekHandler) result(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/cek/hasil/")
	if id == "" || strings.Contains(id, "/") {
		writeCekJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "hasil tidak ditemukan"})
		return
	}
	check, err := h.svc.Result(r.Context(), id)
	if err != nil {
		writeCekJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "hasil ini sudah tidak ada (hasil gratis disimpan 24 jam)"})
		return
	}
	writeCekJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"id":          check.ID,
		"job_title":   check.JobTitle,
		"skor":        check.Score,
		"hasil":       check.Payload,
		"dibuat":      check.CreatedAt.Format(time.RFC3339),
		"kedaluwarsa": check.ExpiresAt.Format(time.RFC3339),
	})
}

func (h *CekHandler) info(w http.ResponseWriter, r *http.Request) {
	quota, err := h.svc.Quota(r.Context(), clientIP(r))
	if err != nil {
		writeCekJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "tidak bisa membaca kuota"})
		return
	}
	writeCekJSON(w, http.StatusOK, map[string]any{"ok": true, "kuota": quota})
}

func (h *CekHandler) interest(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "kiriman tidak bisa dibaca"})
		return
	}
	err := h.svc.Interest(r.Context(), r.FormValue("check_id"), r.FormValue("contact"), r.FormValue("note"))
	if errors.Is(err, services.ErrCekValidation) {
		writeCekJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err != nil {
		log.Printf("[cek] gagal menyimpan minat: %v", err)
		writeCekJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "gagal menyimpan, coba lagi"})
		return
	}
	writeCekJSON(w, http.StatusOK, map[string]any{"ok": true, "pesan": "tercatat — kamu akan dikabari kalau bagian ini dibuka"})
}

// clientIP trusts the forwarding headers because this server binds loopback and
// is reachable only through the reverse proxy that sets them. Exposed directly,
// these headers would be spoofable and the quota would be bypassable.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		parts := strings.Split(v, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func looksLikePDF(data []byte) bool {
	return len(data) > 4 && string(data[:4]) == "%PDF"
}

func writeCekJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("[cek] gagal menulis jawaban: %v", err)
	}
}
