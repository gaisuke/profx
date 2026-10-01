package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TurnstileVerifier checks a Cloudflare Turnstile token. It exists because the
// two open endpoints are the ones that spend model money; it stays disabled
// until a site key is configured, and says so at startup.
type TurnstileVerifier struct {
	Secret string
	client *http.Client
	url    string
}

const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// NewTurnstileVerifier returns nil when no secret is configured, which is the
// signal to skip the check. Callers must not wrap the nil in an interface.
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
