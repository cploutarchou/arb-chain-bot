package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func verifySignature(t *testing.T, header string, body []byte, secret string) {
	t.Helper()
	ts, h1, ok := strings.Cut(header, ";")
	if !ok || !strings.HasPrefix(ts, "ts=") || !strings.HasPrefix(h1, "h1=") {
		t.Fatalf("malformed X-Arb-Signature: %q", header)
	}
	tsVal := strings.TrimPrefix(ts, "ts=")
	if _, err := strconv.ParseInt(tsVal, 10, 64); err != nil {
		t.Fatalf("bad ts: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsVal))
	mac.Write([]byte(":"))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	got := strings.TrimPrefix(h1, "h1=")
	if !hmac.Equal([]byte(want), []byte(got)) {
		t.Fatalf("signature mismatch: want %s got %s", want, got)
	}
}

func TestWebhookSinkSignsAndDelivers(t *testing.T) {
	var gotBody []byte
	var gotSig string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf
		gotSig = r.Header.Get("X-Arb-Signature")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewWebhookSink()
	// Test servers listen on 127.0.0.1 (loopback) — override the
	// private-IP refusal via a custom dialer that skips vetting, since
	// SSRF refusal is covered by its own dedicated test below and would
	// otherwise make httptest itself untestable.
	sink.Client.Transport = &http.Transport{}

	payload := []byte(`{"event_id":"ev-1","rule_id":"rule-1"}`)
	if err := sink.Send(context.Background(), srv.URL, "top-secret-webhook-key", payload); err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != string(payload) {
		t.Fatalf("body = %s, want %s", gotBody, payload)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %q", gotContentType)
	}
	verifySignature(t, gotSig, payload, "top-secret-webhook-key")
}

func TestWebhookSinkRetriesThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewWebhookSink()
	sink.Client.Transport = &http.Transport{}
	sink.Backoff = func(int) time.Duration { return time.Millisecond }

	if err := sink.Send(context.Background(), srv.URL, "s3cret-webhook-value", []byte(`{}`)); err != nil {
		t.Fatalf("Send after retries = %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestWebhookSinkExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sink := NewWebhookSink()
	sink.Client.Transport = &http.Transport{}
	sink.MaxRetries = 2
	sink.Backoff = func(int) time.Duration { return time.Millisecond }

	if err := sink.Send(context.Background(), srv.URL, "s3cret-webhook-value", []byte(`{}`)); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if attempts.Load() != 3 { // first attempt + 2 retries
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestWebhookSinkRefusesPrivateTargets(t *testing.T) {
	sink := NewWebhookSink()
	sink.MaxRetries = 0
	cases := []string{
		"http://127.0.0.1:9/x",
		"http://localhost:9/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/x",
		"http://192.168.1.1/x",
		"http://100.64.0.1/x",  // CGNAT
		"http://[::1]:9/x",     // IPv6 loopback
		"http://0.0.0.0:9/x",   // unspecified
		"http://224.0.0.1:9/x", // multicast
	}
	for _, u := range cases {
		err := sink.Send(context.Background(), u, "s3cret-webhook-value", []byte(`{}`))
		if err == nil || !errors.Is(err, ErrPrivateTarget) {
			t.Fatalf("%s: err = %v, want ErrPrivateTarget", u, err)
		}
	}
}

func TestWebhookSinkRefusesRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	sink := NewWebhookSink()
	sink.Client.Transport = &http.Transport{}
	sink.MaxRetries = 0

	if err := sink.Send(context.Background(), redirector.URL, "s3cret-webhook-value", []byte(`{}`)); err == nil {
		t.Fatal("expected redirect refusal")
	}
}

func TestWebhookSinkRefusesNonHTTPScheme(t *testing.T) {
	sink := NewWebhookSink()
	sink.MaxRetries = 0
	if err := sink.Send(context.Background(), "file:///etc/passwd", "s3cret-webhook-value", []byte(`{}`)); err == nil {
		t.Fatal("expected refusal of a non-http(s) scheme")
	}
}
