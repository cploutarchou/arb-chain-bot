package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

// WebhookTimeout bounds a single delivery attempt (T-086).
const WebhookTimeout = 5 * time.Second

// WebhookMaxRetries is the number of retries after the first attempt.
const WebhookMaxRetries = 3

// WebhookMaxBody caps the response body read (never trusted, only
// logged); refusing to buffer an unbounded response protects the
// dispatch worker from a hostile or misconfigured endpoint.
const WebhookMaxBody = 4 << 10

// ErrPrivateTarget rejects a webhook URL that resolves to a
// loopback/private/link-local/CGNAT/unspecified/multicast address —
// SSRF hardening (T-086): an alert rule is tenant-configured input, so
// its webhook_url must never be able to reach the platform's own
// internal network (including the common 169.254.169.254 metadata
// endpoint, which falls under link-local).
var ErrPrivateTarget = errors.New("notification: webhook target resolves to a private or reserved address")

// ErrRedirect rejects any redirect response — a webhook target must be
// exactly the URL it was configured with; following redirects would
// let a compromised or malicious endpoint retarget the request past
// the SSRF check above.
var ErrRedirect = errors.New("notification: webhook redirects are not followed")

// WebhookSink posts a signed JSON payload to a rule's configured
// webhook_url with retries and a bounded timeout per attempt. It
// implements screener/alerts.WebhookSink; Send is safe to call from a
// bounded worker, never from the evaluator's poll loop directly (a
// synchronous call here, with retries, can take several seconds).
type WebhookSink struct {
	Client     *http.Client
	MaxRetries int
	Backoff    func(attempt int) time.Duration
	Now        func() time.Time
}

// NewWebhookSink builds a WebhookSink whose transport refuses to dial a
// private/reserved address and never follows a redirect. The refusal
// resolves the hostname itself and dials the vetted IP directly
// (net.Dialer never sees the original hostname), so a domain name that
// resolves to a private address is caught exactly like a literal
// private IP in the URL, and DNS rebinding between the check and the
// dial cannot bypass it — the two happen atomically against the same
// resolved address.
func NewWebhookSink() *WebhookSink {
	dialer := &net.Dialer{Timeout: WebhookTimeout}
	resolver := net.DefaultResolver
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := resolveVetted(ctx, resolver, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
	}
	return &WebhookSink{
		Client: &http.Client{
			Transport: transport,
			Timeout:   WebhookTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return ErrRedirect
			},
		},
		MaxRetries: WebhookMaxRetries,
		Backoff:    func(attempt int) time.Duration { return time.Duration(attempt) * time.Second },
		Now:        time.Now,
	}
}

// resolveVetted resolves host to its IP addresses and returns the
// first one that is not private/reserved, refusing (ErrPrivateTarget)
// when every answer is private/reserved. The returned IP — never the
// original hostname — is what the caller dials, so a multi-answer
// response that mixes a public "warm-up" address with a private one
// cannot be used to smuggle the private address past this check by
// racing the resolver.
func resolveVetted(ctx context.Context, resolver *net.Resolver, host string) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if isPrivateOrReserved(ip) {
			return netip.Addr{}, ErrPrivateTarget
		}
		return ip, nil
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if !isPrivateOrReserved(ip) {
			return ip, nil
		}
	}
	return netip.Addr{}, ErrPrivateTarget
}

// isPrivateOrReserved covers loopback, private (RFC1918), link-local
// (which is where 169.254.169.254 — the common cloud metadata endpoint
// — lives), CGNAT (100.64.0.0/10), unspecified and multicast, for both
// IPv4 and IPv6 (including IPv4-mapped IPv6).
func isPrivateOrReserved(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip.Is4() {
		b := ip.As4()
		// 100.64.0.0/10 (CGNAT / carrier-grade NAT).
		if b[0] == 100 && b[1]&0xc0 == 64 {
			return true
		}
	}
	return false
}

// Sign produces an X-Arb-Signature header value: "ts=<unix>;h1=<hex>"
// over "<ts>:<body>", HMAC-SHA256 with the rule's webhook_secret —
// deliberately the same shape as internal/billing/paddle.Sign, the
// repo's one existing outbound-webhook-signature precedent.
func Sign(body []byte, secret string, at time.Time) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte(":"))
	mac.Write(body)
	return "ts=" + ts + ";h1=" + hex.EncodeToString(mac.Sum(nil))
}

// Send posts payload to url with retries and backoff, refusing
// non-http(s) schemes outright before ever touching the network. It
// blocks for up to MaxRetries*(Timeout+Backoff) — callers dispatch it
// off the hot path.
func (w *WebhookSink) Send(ctx context.Context, url, secret string, payload []byte) error {
	now := w.Now
	if now == nil {
		now = time.Now
	}
	retries := w.MaxRetries
	if retries < 0 {
		retries = 0
	}
	backoff := w.Backoff
	if backoff == nil {
		backoff = func(attempt int) time.Duration { return time.Duration(attempt) * time.Second }
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff(attempt)):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := w.attempt(ctx, url, secret, payload, now()); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("notification: webhook delivery failed after %d attempt(s): %w", retries+1, lastErr)
}

func (w *WebhookSink) attempt(ctx context.Context, rawURL, secret string, payload []byte, at time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Arb-Signature", Sign(payload, secret, at))
	resp, err := w.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, WebhookMaxBody)); _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification: webhook endpoint returned %d", resp.StatusCode)
	}
	return nil
}
