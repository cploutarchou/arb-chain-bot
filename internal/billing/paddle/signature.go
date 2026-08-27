package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureTolerance bounds the age of a webhook (replay window).
const SignatureTolerance = 5 * time.Minute

// VerifySignature checks the Paddle-Signature header against the raw
// body: header is "ts=<unix>;h1=<hex>" (several h1 values during a
// secret rotation), the signed payload is "<ts>:<body>", the MAC is
// HMAC-SHA256 with the notification destination's secret. Comparison is
// constant-time; the timestamp must be within SignatureTolerance of now.
func VerifySignature(header string, body []byte, secret string, now time.Time) error {
	if secret == "" {
		return ErrNotConfigured
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "ts":
			ts = v
		case "h1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return fmt.Errorf("%w: malformed header", ErrBadSignature)
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad timestamp", ErrBadSignature)
	}
	if d := now.Sub(time.Unix(unix, 0)); d > SignatureTolerance || d < -SignatureTolerance {
		return ErrStaleSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte(":"))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err != nil {
			continue
		}
		if hmac.Equal(got, want) {
			return nil
		}
	}
	return ErrBadSignature
}

// Sign produces a Paddle-Signature header (tests and the sandbox replay
// tool).
func Sign(body []byte, secret string, at time.Time) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte(":"))
	mac.Write(body)
	return "ts=" + ts + ";h1=" + hex.EncodeToString(mac.Sum(nil))
}
