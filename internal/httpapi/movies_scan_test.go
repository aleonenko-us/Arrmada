package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// A second Scan while one runs is a clear 409 naming the running job, so the page follows
// that scan's progress instead of starting another (MOV-20).
func TestConcurrentScanRejected(t *testing.T) {
	fj := newFakeJobs(false)
	s := newRouteServer(t, func(d *Deps) { d.Jobs = fj })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	if rec := s.do("POST", "/api/v1/movies/scan", mgr); rec.Code != http.StatusAccepted {
		t.Fatalf("first scan: HTTP %d %s", rec.Code, rec.Body)
	}
	rec := s.do("POST", "/api/v1/movies/scan", mgr)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || body["message"] != "a library scan is already running" || body["job_id"] != float64(1) {
		t.Fatalf("second scan: HTTP %d %v", rec.Code, body)
	}
	if n := len(fj.specs()); n != 1 {
		t.Fatalf("%d scans submitted, want 1", n)
	}
	// The budget grows with the library and is never under 15 minutes.
	if got := fj.specs()[0].Timeout; got.Minutes() < 15 {
		t.Errorf("scan timeout = %v, want at least 15m", got)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1204: "1,204", 1234567: "1,234,567", -1204: "-1,204"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
