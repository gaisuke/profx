package llm

import (
	"strings"
	"testing"
)

func TestParseJSONResponse(t *testing.T) {
	type payload struct {
		MatchRate float64 `json:"cv_match_rate"`
		Feedback  string  `json:"cv_feedback"`
	}
	cases := []struct {
		name    string
		in      string
		want    float64
		wantErr string
	}{
		{"bare json", `{"cv_match_rate":0.85,"cv_feedback":"ok"}`, 0.85, ""},
		{"markdown fence", "```json\n{\"cv_match_rate\":0.7,\"cv_feedback\":\"ok\"}\n```", 0.7, ""},
		{"prose around json", "Here is the evaluation:\n{\"cv_match_rate\":0.6,\"cv_feedback\":\"ok\"}\nHope it helps.", 0.6, ""},
		{"nested object", `{"cv_match_rate":0.9,"cv_feedback":"ok","extra":{"a":1}}`, 0.9, ""},
		{"truncated output", `{"cv_match_rate":0.9,"cv_feedback":"long...`, 0, "invalid JSON"},
		{"no json at all", "I cannot evaluate this candidate.", 0, "invalid JSON"},
	}
	for _, tc := range cases {
		var got payload
		err := ParseJSONResponse(tc.in, &got)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: expected error containing %q, got %v", tc.name, tc.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got.MatchRate != tc.want {
			t.Errorf("%s: match rate = %v, want %v", tc.name, got.MatchRate, tc.want)
		}
	}
}

func TestExtractJSONObject(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"with prefix", `text {"a":1} tail`, `{"a":1}`},
		{"nested braces", `{"a":{"b":2}}`, `{"a":{"b":2}}`},
		{"unterminated", `{"a":1`, `{"a":1`},
		{"no object", `nothing`, `nothing`},
	}
	for _, tc := range cases {
		if got := extractJSONObject(tc.in); got != tc.want {
			t.Errorf("%s: extractJSONObject(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
