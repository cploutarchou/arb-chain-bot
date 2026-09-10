package notification

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// fakeTransport records the composed message instead of touching the
// network — the seam EmailSink.Send needs to be testable at all.
type fakeTransport struct {
	from string
	to   []string
	msg  []byte
	err  error
	n    int
}

func (f *fakeTransport) Send(_ context.Context, from string, to []string, msg []byte) error {
	f.n++
	f.from, f.to, f.msg = from, to, msg
	return f.err
}

func TestEmailSinkComposesAndSends(t *testing.T) {
	ft := &fakeTransport{}
	sink := &EmailSink{Transport: ft, From: "alerts@example.test"}
	if err := sink.Send(context.Background(), "trader@example.test", "Screener rule fired", "spread 50 bps\n\nfooter text"); err != nil {
		t.Fatal(err)
	}
	if ft.n != 1 {
		t.Fatalf("transport called %d times, want 1", ft.n)
	}
	if ft.from != "alerts@example.test" || len(ft.to) != 1 || ft.to[0] != "trader@example.test" {
		t.Fatalf("envelope = from=%q to=%v", ft.from, ft.to)
	}
	msg := string(ft.msg)
	for _, want := range []string{"From: alerts@example.test", "To: trader@example.test", "Subject: Screener rule fired", "spread 50 bps"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestEmailSinkDefaultFrom(t *testing.T) {
	ft := &fakeTransport{}
	sink := &EmailSink{Transport: ft}
	if err := sink.Send(context.Background(), "trader@example.test", "s", "b"); err != nil {
		t.Fatal(err)
	}
	if ft.from == "" {
		t.Fatal("expected a default From address")
	}
}

func TestEmailSinkRejectsInvalidRecipient(t *testing.T) {
	ft := &fakeTransport{}
	sink := &EmailSink{Transport: ft}
	if err := sink.Send(context.Background(), "not-an-email", "s", "b"); err == nil {
		t.Fatal("expected an error for an invalid recipient")
	}
	if ft.n != 0 {
		t.Fatal("transport must not be called for an invalid recipient")
	}
}

func TestEmailSinkNotConfigured(t *testing.T) {
	sink := &EmailSink{}
	if err := sink.Send(context.Background(), "trader@example.test", "s", "b"); !errors.Is(err, ErrSMTPNotConfigured) {
		t.Fatalf("err = %v, want ErrSMTPNotConfigured", err)
	}
}

func TestEmailSinkPropagatesTransportError(t *testing.T) {
	sentinel := errors.New("smtp: connection refused")
	ft := &fakeTransport{err: sentinel}
	sink := &EmailSink{Transport: ft}
	if err := sink.Send(context.Background(), "trader@example.test", "s", "b"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

// TestEmailSinkStripsHeaderInjection: Subject/To/From must never let a
// CR/LF-bearing value split into an extra header LINE — the injected
// text may still appear as harmless words inside the Subject line, but
// it must never start its own "\r\nBcc: ..." line.
func TestEmailSinkStripsHeaderInjection(t *testing.T) {
	ft := &fakeTransport{}
	sink := &EmailSink{Transport: ft, From: "alerts@example.test"}
	subject := "Rule fired\r\nBcc: attacker@evil.test"
	if err := sink.Send(context.Background(), "trader@example.test", subject, "body"); err != nil {
		t.Fatal(err)
	}
	msg := string(ft.msg)
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("header injection produced a Bcc header line: %s", msg)
		}
	}
	if !strings.Contains(msg, "Subject: Rule fired  Bcc: attacker@evil.test") {
		t.Fatalf("expected the injected text folded harmlessly into Subject: %s", msg)
	}
}

func TestSMTPTransportNotConfiguredWhenURLEmpty(t *testing.T) {
	tr := &SMTPTransport{URL: func(context.Context) string { return "" }}
	if err := tr.Send(context.Background(), "a@example.test", []string{"b@example.test"}, []byte("x")); !errors.Is(err, ErrSMTPNotConfigured) {
		t.Fatalf("err = %v, want ErrSMTPNotConfigured", err)
	}
}

func TestSMTPTransportDialFailureIsWrapped(t *testing.T) {
	// Port 9 ("discard") on loopback refuses connections in virtually
	// every sandbox; the assertion only needs a non-nil, descriptive
	// error, not a specific network outcome.
	tr := &SMTPTransport{URL: func(context.Context) string { return "smtp://user:pass@127.0.0.1:9" }}
	if err := tr.Send(context.Background(), "a@example.test", []string{"b@example.test"}, []byte("x")); err == nil {
		t.Fatal("expected a dial error")
	}
}

// TestSMTPTransportMalformedURLNeverLeaksCredential is the regression
// for audit S2/P1-11: url.Parse's *url.Error embeds the exact string it
// failed to parse, so wrapping it used to put the smtp_url password
// into whatever the caller did with the returned error. The caller
// (screener/alerts dispatch) both logs the error and persists its
// .Error() verbatim as a delivery outcome's Reason (later served by an
// authenticated read route to any viewer) — so this asserts the secret
// is unrecoverable from every one of those uses, not just the error
// value itself.
func TestSMTPTransportMalformedURLNeverLeaksCredential(t *testing.T) {
	const password = "S3cr3tP%zzPass" // "%zz" is not a valid percent-escape: url.Parse fails on it
	malformed := "smtp://alerts:" + password + "@smtp.example.test:587"
	tr := &SMTPTransport{URL: func(context.Context) string { return malformed }}

	err := tr.Send(context.Background(), "a@example.test", []string{"b@example.test"}, []byte("x"))
	if err == nil {
		t.Fatal("malformed smtp_url must fail to send")
	}
	if !errors.Is(err, ErrInvalidSMTPURL) {
		t.Fatalf("err = %v, want ErrInvalidSMTPURL", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("returned error leaks the password: %v", err)
	}

	// "no log line": a buffer-backed handler stands in for the real
	// logger the dispatch path uses.
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Warn("screener alerts: channel delivery failed", "channel", "email", "error", err)
	if strings.Contains(buf.String(), password) {
		t.Fatalf("log line leaks the password: %s", buf.String())
	}

	// "no persisted reason": the dispatch path stores err.Error() as-is.
	if reason := err.Error(); strings.Contains(reason, password) {
		t.Fatalf("persisted reason leaks the password: %s", reason)
	}
}
