package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReportDetailAndCSVRoutes covers BL-32's new detail/CSV routes:
// PermReportView gates both (viewer holds it), and without a store the
// routes answer an honest 404 rather than panicking on a nil *Store.
func TestReportDetailAndCSVRoutes(t *testing.T) {
	_, mux := newTestServer(t)
	viewerCookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	for _, path := range []string{
		"/api/v1/reports/rep-1",
		"/api/v1/reports/rep-1/csv",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unauth GET %s = %d", path, rec.Code)
		}

		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(viewerCookie)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("viewer GET %s without store = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}
