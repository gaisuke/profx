package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The request/response shapes below are the ones the live gateway uses: the
// Anthropic Messages format, with reasoning blocks alongside text blocks.
func anthropicReply(text, stopReason string) string {
	return fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":%q}],"stop_reason":%q}`,
		text, stopReason)
}

func TestOpenCodeGoClientParsesTextAndSendsRequiredHeaders(t *testing.T) {
	var gotPath, gotKey, gotVersion, gotSession, gotUA string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotSession = r.Header.Get("x-opencode-session")
		gotUA = r.Header.Get("User-Agent")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		fmt.Fprint(rw, anthropicReply("{\"cv_match_rate\":0.8}", "end_turn"))
	}))
	defer srv.Close()

	c := NewOpenCodeGoClient("secret-key", srv.URL, "deepseek-v4.1-flash", 4096)
	out, err := c.Generate(context.Background(), "evaluate this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"cv_match_rate":0.8}` {
		t.Fatalf("text block not extracted, got %q", out)
	}
	if gotPath != "/messages" {
		t.Errorf("path = %q, want /messages", gotPath)
	}
	if gotKey != "secret-key" || gotVersion == "" || gotSession == "" {
		t.Errorf("missing required headers: key=%q version=%q session=%q", gotKey, gotVersion, gotSession)
	}
	if gotUA == "" || gotUA[:5] != "Mozil" {
		t.Errorf("browser User-Agent required to pass Cloudflare, got %q", gotUA)
	}
	if body["model"] != "deepseek-v4.1-flash" || body["temperature"] != 0.2 {
		t.Errorf("unexpected request body: %v", body)
	}
}

func TestOpenCodeGoClientGrowsTokenBudgetOnReasoningTruncation(t *testing.T) {
	var budgets []float64
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		budgets = append(budgets, body["max_tokens"].(float64))
		if len(budgets) == 1 {
			// budget spent entirely on hidden reasoning: no text at all
			fmt.Fprint(rw, anthropicReply("", "max_tokens"))
			return
		}
		fmt.Fprint(rw, anthropicReply(`{"cv_match_rate":0.7}`, "end_turn"))
	}))
	defer srv.Close()

	c := NewOpenCodeGoClient("k", srv.URL, "", 2500)
	out, err := c.Generate(context.Background(), "evaluate this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"cv_match_rate":0.7}` {
		t.Fatalf("got %q", out)
	}
	if len(budgets) != 2 || budgets[1] != budgets[0]*2 {
		t.Fatalf("token budget should double after truncation, got %v", budgets)
	}
}

func TestOpenCodeGoClientRetriesTransientFailureButNotClientError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			rw.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(rw, `{"error":{"type":"rate_limit","message":"slow down"}}`)
			return
		}
		fmt.Fprint(rw, anthropicReply(`{"ok":true}`, "end_turn"))
	}))
	defer srv.Close()

	c := NewOpenCodeGoClient("k", srv.URL, "", 100)
	if _, err := c.GenerateWithRetry(context.Background(), "p", 3); err != nil {
		t.Fatalf("429 should be retried, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}

	permanent := 0
	srv2 := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		permanent++
		rw.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(rw, `{"error":{"type":"invalid_request","message":"bad model"}}`)
	}))
	defer srv2.Close()
	c2 := NewOpenCodeGoClient("k", srv2.URL, "", 100)
	if _, err := c2.GenerateWithRetry(context.Background(), "p", 3); err == nil {
		t.Fatal("400 must fail")
	}
	if permanent != 1 {
		t.Fatalf("a 400 must not be retried, calls = %d", permanent)
	}
}

func TestOpenCodeGoClientReportsEmptyReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		fmt.Fprint(rw, anthropicReply("", "end_turn"))
	}))
	defer srv.Close()

	c := NewOpenCodeGoClient("k", srv.URL, "", 100)
	if _, err := c.GenerateWithRetry(context.Background(), "p", 1); err == nil {
		t.Fatal("an empty reply must be an error, not an empty string")
	}
}
