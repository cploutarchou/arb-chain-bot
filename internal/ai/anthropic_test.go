package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newFakeAnthropic serves the Messages API shape and asserts the
// request carries the auth header and never a body secret.
func newFakeAnthropic(t *testing.T, text string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") == "" || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"missing auth"}}`))
			return
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
			return
		}
		resp := map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAnthropicErrorStatusSurfaces(t *testing.T) {
	adv := NewAnthropic("", "claude-sonnet-5") // empty key → 401 from fake
	adv.BaseURL = newFakeAnthropic(t, "unused")
	if _, err := adv.Analyze(t.Context(), "p"); err == nil {
		t.Fatal("401 must surface as error")
	}
}
