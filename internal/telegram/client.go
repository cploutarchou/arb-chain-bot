// Package telegram is the Telegram control surface (SKILL.md §56–§59,
// resources/telegram.md): a long-polling bot with allow-list auth,
// strict command parsing, signed inline-button callbacks, and pushes
// delivered only through the notification service. Handlers call the
// same application services as the web console; no trading, risk, or
// config logic lives here.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client is a minimal Bot API client. BaseURL includes the token path
// ("https://api.telegram.org/bot<token>"); tests point it at a fake
// server, so no real token or egress is ever needed.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 65 * time.Second}}
}

// Update is one long-poll result entry.
type Update struct {
	UpdateID int64          `json:"update_id"`
	Message  *Message       `json:"message"`
	Callback *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// InlineKeyboard is the reply_markup shape for inline buttons.
type InlineKeyboard struct {
	Rows [][]InlineButton
}

type InlineButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

func (k *InlineKeyboard) marshal() json.RawMessage {
	if k == nil || len(k.Rows) == 0 {
		return nil
	}
	body, _ := json.Marshal(struct {
		InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
	}{k.Rows})
	return body
}

type apiEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/"+method, bytes.NewBufferString(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("telegram: %s decode: %w", method, err)
	}
	if !env.OK {
		return fmt.Errorf("telegram: %s failed: %s", method, env.Description)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	params := url.Values{}
	params.Set("offset", strconv.FormatInt(offset, 10))
	params.Set("timeout", strconv.Itoa(int(timeout.Seconds())))
	params.Set("allowed_updates", `["message","callback_query"]`)
	var updates []Update
	err := c.call(ctx, "getUpdates", params, &updates)
	return updates, err
}

// SendMessage sends text (plain; no HTML/Markdown parsing so untrusted
// content can never smuggle formatting) with optional inline buttons.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboard) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("text", text)
	if raw := kb.marshal(); raw != nil {
		params.Set("reply_markup", string(raw))
	}
	return c.call(ctx, "sendMessage", params, nil)
}

// AnswerCallback acks a button tap (with optional toast text).
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	params := url.Values{}
	params.Set("callback_query_id", callbackID)
	if text != "" {
		params.Set("text", text)
	}
	return c.call(ctx, "answerCallbackQuery", params, nil)
}
