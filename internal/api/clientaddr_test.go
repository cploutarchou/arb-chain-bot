package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

func newClientAddrServer(t *testing.T, trusted ...string) *Server {
	t.Helper()
	var prefixes []netip.Prefix
	for _, s := range trusted {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				t.Fatalf("bad test fixture %q: %v / %v", s, err, aerr)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		prefixes = append(prefixes, p)
	}
	cfg := config.Bootstrap{Mode: config.ModeMarketData, HTTPAddr: ":0", TrustedProxies: prefixes}
	return NewServer(cfg, discardLogger(), BuildInfo{Version: "test"})
}

// Acceptance (audit S1/P1-10): without any trusted proxy configured,
// X-Forwarded-For must be ignored entirely — the resolved address is
// always the raw TCP peer, so a caller cannot forge it just by sending
// the header.
func TestClientAddrIgnoresHeaderWithNoTrustedProxies(t *testing.T) {
	s := newClientAddrServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Forwarded-For", "198.51.100.50")

	got := s.clientAddr(req)
	if got.String() != "203.0.113.9" {
		t.Fatalf("clientAddr = %s, want the raw peer 203.0.113.9", got)
	}
}

// Acceptance: the header is ignored from an UNTRUSTED peer even when
// the server does trust some other proxy — a direct attacker cannot
// spoof their address just because a real proxy exists elsewhere.
func TestClientAddrIgnoresHeaderFromUntrustedPeer(t *testing.T) {
	s := newClientAddrServer(t, "10.0.0.0/8")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:5555" // not inside 10.0.0.0/8
	req.Header.Set("X-Forwarded-For", "198.51.100.50")

	got := s.clientAddr(req)
	if got.String() != "203.0.113.9" {
		t.Fatalf("clientAddr = %s, want the raw (untrusted) peer 203.0.113.9", got)
	}
}

// Acceptance: honoured from a trusted peer — the rightmost entry that
// is not itself a trusted proxy is the resolved client.
func TestClientAddrHonoursHeaderFromTrustedPeer(t *testing.T) {
	s := newClientAddrServer(t, "10.0.0.0/8")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:5555" // the trusted ingress
	req.Header.Set("X-Forwarded-For", "198.51.100.50")

	got := s.clientAddr(req)
	if got.String() != "198.51.100.50" {
		t.Fatalf("clientAddr = %s, want the forwarded client 198.51.100.50", got)
	}
}

// A chain of several trusted hops: the rightmost entry that is NOT a
// trusted proxy is picked, skipping intermediate trusted hops.
func TestClientAddrSkipsTrustedHopsInChain(t *testing.T) {
	s := newClientAddrServer(t, "10.0.0.0/8")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:5555"
	req.Header.Set("X-Forwarded-For", "198.51.100.50, 10.0.0.9")

	got := s.clientAddr(req)
	if got.String() != "198.51.100.50" {
		t.Fatalf("clientAddr = %s, want 198.51.100.50 (skipping the trusted internal hop)", got)
	}
}

// X-Real-IP is honoured, from a trusted peer, when X-Forwarded-For is
// absent.
func TestClientAddrFallsBackToXRealIP(t *testing.T) {
	s := newClientAddrServer(t, "10.0.0.0/8")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:5555"
	req.Header.Set("X-Real-IP", "198.51.100.60")

	got := s.clientAddr(req)
	if got.String() != "198.51.100.60" {
		t.Fatalf("clientAddr = %s, want the X-Real-IP value 198.51.100.60", got)
	}
}
