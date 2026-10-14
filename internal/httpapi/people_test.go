package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// peopleKeys is every key a /people row may carry. A new key has to be added here on
// purpose, after checking it says nothing about what anyone listens to.
var peopleKeys = map[string]bool{
	"id": true, "username": true, "role": true, "disabled": true, "created_at": true,
	"auto_approve_movie": true, "auto_approve_series": true, "auto_approve_book": true,
	"plex_linked": true, "plex_username": true, "plex_only": true, "plex_blocked": true, "quota": true,
	"jellyfin_linked": true, "jellyfin_username": true, "jellyfin_only": true, "jellyfin_blocked": true,
	"sessions": true, "web_last": true, "audio_last": true, "listen_last": true, "last_seen": true,
	"audio_eligible": true, "audio_allowed": true, "audio_password_set": true, "audio_devices": true,
}

// seedListening gives userID a place, a place history, a play session and a listening-log
// row on itemKey (all distinctively named), plus a signed-in listening app last used at
// appAt and listening that ended at listenAt.
func seedListening(t *testing.T, h *audioHarness, userID int64, itemKey string, appAt, listenAt int64) {
	t.Helper()
	db := h.rs.st.DB()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO listen_progress (user_id, item_key, position, duration, updated_at, device) VALUES (?, ?, 100, 1000, ?, 'Pixel')`, []any{userID, itemKey, listenAt}},
		{`INSERT INTO listen_history (user_id, item_key, position, at, device, reason) VALUES (?, ?, 50, ?, 'Pixel', 'sync')`, []any{userID, itemKey, listenAt}},
		{`INSERT INTO listen_sessions (id, user_id, item_key, device, client, started_at, last_at) VALUES ('sess-` + itemKey + `', ?, ?, 'Pixel', 'Lissen', ?, ?)`, []any{userID, itemKey, listenAt - 600_000, listenAt}},
		{`INSERT INTO listen_log (session_id, user_id, device, client, started_at, ended_at, seconds) VALUES ('sess-` + itemKey + `', ?, 'Pixel', 'Lissen', ?, ?, 600)`, []any{userID, listenAt - 600_000, listenAt}},
		{`INSERT INTO audio_tokens (user_id, hash, kind, family, client, device, created_at, last_used_at) VALUES (?, 'hash-` + itemKey + `', 'refresh', 'fam-` + itemKey + `', 'Lissen', 'Pixel', ?, ?)`, []any{userID, appAt - 1000, appAt}},
	} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
}

// The people list says when and how much, never what: none of the seeded item keys or the
// book's title appear, and every row's keys are on the allowlist.
func TestPeopleListNoListeningContent(t *testing.T) {
	h := newAudioHarness(t)
	_, adminC := h.rs.user(t, "owner", auth.RoleAdmin)
	kid, _ := h.rs.user(t, "kid", auth.RoleRequester)
	now := time.Now().UnixMilli()
	seedListening(t, h, kid.ID, "item-SECRET-KEY-31337", now-3_600_000, now-7_200_000)
	seedListening(t, h, kid.ID, h.key, now-3_000_000, now-6_000_000)

	rec := h.req("GET", "/api/v1/people", nil, adminC, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, secret := range []string{"item-SECRET-KEY-31337", h.key, h.title, "Dinniman", "position", "item"} {
		if strings.Contains(body, secret) {
			t.Errorf("the people list carries %q:\n%s", secret, body)
		}
	}
	var out struct {
		People []map[string]json.RawMessage `json:"people"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.People) != 2 {
		t.Fatalf("people = %s (%v)", body, err)
	}
	for _, p := range out.People {
		for k := range p {
			if !peopleKeys[k] {
				t.Errorf("unexpected key %q in a people row", k)
			}
		}
	}
}

// Last seen is the latest of the web app, a listening app and listening itself, each also
// reported on its own; sessions counts only live browser sessions; audiobook access and
// the password marker follow the audiobook server.
func TestPeopleLastSeenFromSessionsAndAudio(t *testing.T) {
	h := newAudioHarness(t)
	ctx := context.Background()
	_, adminC := h.rs.user(t, "owner", auth.RoleAdmin)
	kid, _ := h.rs.user(t, "kid", auth.RoleRequester) // one session, from the helper's sign-in
	if _, _, err := h.rs.auth.CreateSessionFrom(ctx, kid.ID, auth.SessionClient{UserAgent: "Firefox"}); err != nil {
		t.Fatal(err)
	}
	quiet, _ := h.rs.user(t, "quiet", auth.RoleReadonly)
	if _, err := h.rs.auth.RevokeUserSessions(ctx, quiet.ID); err != nil {
		t.Fatal(err)
	}
	// The kid's listening app was last used 2 h ago, and their listening ended 1 h ago.
	now := time.Now().UnixMilli()
	seedListening(t, h, kid.ID, "k1", now-2*3_600_000, now-3_600_000)
	if err := h.srv.Accounts.SetPassword(ctx, kid.ID, "audiobooks1", false); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.SetAllowed(ctx, quiet.ID, false); err != nil {
		t.Fatal(err)
	}

	var out struct {
		People []personRow `json:"people"`
	}
	rec := h.req("GET", "/api/v1/people", nil, adminC, false)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	byName := map[string]personRow{}
	for _, p := range out.People {
		byName[p.Username] = p
	}
	k := byName["kid"]
	if k.Sessions != 2 {
		t.Errorf("kid sessions = %d, want 2", k.Sessions)
	}
	if k.WebLast < now-60_000 {
		t.Errorf("kid web_last = %d, want about now (%d)", k.WebLast, now)
	}
	if k.AudioLast != now-2*3_600_000 || k.ListenLast != now-3_600_000 || k.AudioDevices != 1 {
		t.Errorf("kid audio_last = %d, listen_last = %d, devices = %d", k.AudioLast, k.ListenLast, k.AudioDevices)
	}
	if k.LastSeen != max(k.WebLast, k.AudioLast, k.ListenLast) || k.LastSeen != k.WebLast {
		t.Errorf("kid last_seen = %d, want the web session's %d", k.LastSeen, k.WebLast)
	}
	if !k.AudioPasswordSet || !k.AudioAllowed || !k.AudioEligible {
		t.Errorf("kid audio = %+v", k)
	}
	q := byName["quiet"]
	if q.Sessions != 0 || q.LastSeen != 0 || q.AudioAllowed || q.AudioEligible || q.AudioPasswordSet {
		t.Errorf("quiet = %+v, want never seen, switched off, read-only", q)
	}

	// Listening later than any web use moves last seen.
	h2 := now + 60_000
	seedListening(t, h, kid.ID, "k2", h2-1000, h2)
	_ = json.Unmarshal(h.req("GET", "/api/v1/people", nil, adminC, false).Body.Bytes(), &out)
	for _, p := range out.People {
		if p.Username == "kid" && p.LastSeen != h2 {
			t.Errorf("kid last_seen = %d after later listening, want %d", p.LastSeen, h2)
		}
	}
}

// Only an admin may list people.
func TestPeopleAdminOnly(t *testing.T) {
	h := newAudioHarness(t)
	for _, role := range []auth.Role{auth.RoleManager, auth.RoleRequester, auth.RoleReadonly} {
		_, c := h.rs.user(t, "u-"+string(role), role)
		if rec := h.req("GET", "/api/v1/people", nil, c, false); rec.Code != http.StatusForbidden {
			t.Errorf("%s: HTTP %d, want 403", role, rec.Code)
		}
	}
}
