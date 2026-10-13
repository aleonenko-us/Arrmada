package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/applog"
	"github.com/tristenlammi/arrmada/internal/auth"
)

// The debug switch is the admin's alone, says when it ends, and turning it back is one
// call; a bad level or duration is refused rather than guessed at.
func TestLogLevelRoutes(t *testing.T) {
	sw := applog.NewLevelSwitch(slog.LevelInfo)
	t.Cleanup(sw.Reset)
	s := newRouteServer(t, func(d *Deps) { d.LogLevel = sw })
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	if rec := s.do("GET", "/api/v1/logs/level", mgr); rec.Code != http.StatusForbidden {
		t.Errorf("manager GET: HTTP %d, want 403", rec.Code)
	}
	if rec := s.doJSON("PUT", "/api/v1/logs/level", mgr, `{"level":"debug"}`); rec.Code != http.StatusForbidden {
		t.Errorf("manager PUT: HTTP %d, want 403", rec.Code)
	}
	if sw.State().Level != slog.LevelInfo {
		t.Fatal("a refused PUT changed the level")
	}

	read := func(body []byte) logLevelView {
		t.Helper()
		var v logLevelView
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return v
	}

	rec := s.doJSON("PUT", "/api/v1/logs/level", admin, `{"level":"debug","minutes":30}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT: HTTP %d %s", rec.Code, rec.Body)
	}
	v := read(rec.Body.Bytes())
	if v.Level != "debug" || v.EnvLevel != "info" || v.Until == nil {
		t.Fatalf("after PUT = %+v, want debug until a deadline, env info", v)
	}
	if left := time.Until(*v.Until); left < 29*time.Minute || left > 30*time.Minute {
		t.Errorf("deadline %v away, want ~30 min", left)
	}
	if got := read(s.do("GET", "/api/v1/logs/level", admin).Body.Bytes()); got.Level != "debug" || got.Until == nil {
		t.Errorf("GET = %+v, want the raised level", got)
	}

	for _, body := range []string{`{"level":"loud"}`, `{"level":"debug","minutes":241}`, `{"level":"debug","minutes":-1}`} {
		if rec := s.doJSON("PUT", "/api/v1/logs/level", admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: HTTP %d, want 400", body, rec.Code)
		}
	}

	rec = s.doJSON("PUT", "/api/v1/logs/level", admin, `{"level":""}`)
	if v := read(rec.Body.Bytes()); rec.Code != http.StatusOK || v.Level != "info" || v.Until != nil {
		t.Errorf("back to normal: HTTP %d %+v", rec.Code, v)
	}
}
