package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Anthropic calls the Messages API directly (no SDK dependency). The
// key travels only in the request header — never logged, never stored,
// never echoed into results.
type Anthropic struct {
	APIKey  string
	ModelID string
	BaseURL string // default https://api.anthropic.com; injectable for tests
	HTTP    *http.Client
	// MaxTokens is the request's max_tokens (platform ai.budget.
	// max_output_tokens); 0 falls back to the pre-T-059 constant 2048.
	MaxTokens int
}

func NewAnthropic(apiKey, model string) *Anthropic {
	return &Anthropic{
		APIKey: apiKey, ModelID: model,
		BaseURL: "https://api.anthropic.com",
		HTTP:    &http.Client{Timeout: 90 * time.Second},
	}
}

func (a *Anthropic) Name() string  { return "anthropic" }
func (a *Anthropic) Model() string { return a.ModelID }

func (a *Anthropic) Analyze(ctx context.Context, prompt string) (string, error) {
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	body, err := json.Marshal(map[string]any{
		"model":      a.ModelID,
		"max_tokens": maxTokens,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		// Error bodies are provider diagnostics, not secrets; bounded.
		return "", fmt.Errorf("ai: anthropic status %d: %s", resp.StatusCode, truncate(string(payload), 300))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", fmt.Errorf("ai: anthropic decode: %w", err)
	}
	for _, c := range out.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", fmt.Errorf("ai: anthropic response had no text content")
}
