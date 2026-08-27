package api

import (
	"net/http"
	"testing"
)

func TestPnLAndAnalyticsRoutesRequireStore(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	for _, path := range []string{
		"/api/v1/pnl/breakdown?by=triangle",
		"/api/v1/pnl/series",
		"/api/v1/analytics/distributions",
	} {
		rec := getWith(t, mux, cookie, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s without store = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}
