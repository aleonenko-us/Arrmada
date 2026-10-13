package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// A connection's stream filters are saved with it, shown back, kept by an edit that
// leaves them out (renaming, a new webhook), and checked on the way in.
func TestNotificationOptionsRoundTrip(t *testing.T) {
	s, _ := newNotifyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)

	rec := s.doJSON("POST", "/api/v1/notifications", admin,
		`{"name":"Streams","url":"ntfy://topic","enabled":true,"events":["plex.stream.started"],"options":{"stream_users":[" 42 ","42","7"],"remote_only":true}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: HTTP %d %s", rec.Code, rec.Body)
	}
	c, err := s.deps.Notify.Get(context.Background(), 1)
	if err != nil || !c.Options.RemoteOnly || len(c.Options.StreamUsers) != 2 || c.Options.StreamUsers[0] != "42" {
		t.Fatalf("stored options = %+v, %v", c.Options, err)
	}
	list := listNotifications(t, s, admin)
	if opts, _ := list[0]["options"].(map[string]any); opts == nil || opts["remote_only"] != true {
		t.Fatalf("listed options = %v", list[0]["options"])
	}

	// An edit without options keeps them.
	if rec := s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"Renamed","kind":"","enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, _ = s.deps.Notify.Get(context.Background(), 1); !c.Options.RemoteOnly || len(c.Options.StreamUsers) != 2 {
		t.Fatalf("options lost on rename: %+v", c.Options)
	}
	// An edit with options replaces them.
	if rec := s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"Renamed","kind":"","enabled":true,"options":{"transcode_only":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("update: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, _ = s.deps.Notify.Get(context.Background(), 1); c.Options.RemoteOnly || !c.Options.TranscodeOnly || len(c.Options.StreamUsers) != 0 {
		t.Fatalf("options not replaced: %+v", c.Options)
	}
	if rec := s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"Renamed","kind":"","enabled":true,"options":{"stream_users":["a\u0000b"]}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad user id: HTTP %d, want 400", rec.Code)
	}
}
