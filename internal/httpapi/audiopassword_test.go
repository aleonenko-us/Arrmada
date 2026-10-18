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

// Setting the audiobook password asks for what making an API key asks for: the account
// password, a two-factor code when that's on, and (no password to ask for) a recent
// sign-in. A browser left signed in isn't enough to hand someone a lasting way in.
func TestAudioPasswordNeedsStepUp(t *testing.T) {
	h := newOPDSHarness(t)
	s := h.rs
	ctx := context.Background()
	u, c := s.user(t, "kid", auth.RoleRequester) // password123
	put := func(body string) *http.Response {
		rec := s.doJSON("PUT", "/api/v1/me/audio/password", c, body)
		return rec.Result()
	}
	var mine map[string]any
	_ = json.Unmarshal(s.do("GET", "/api/v1/me/audio", c).Body.Bytes(), &mine)
	if mine["password_required"] != true || mine["code_required"] != false || mine["reauth_required"] != false {
		t.Fatalf("GET /me/audio step-up = %v %v %v", mine["password_required"], mine["code_required"], mine["reauth_required"])
	}
	if res := put(`{"password":"listen-long-1"}`); res.StatusCode != http.StatusBadRequest {
		t.Errorf("no account password: HTTP %d, want 400", res.StatusCode)
	}
	if res := put(`{"password":"listen-long-1","current_password":"nope-nope"}`); res.StatusCode != http.StatusBadRequest {
		t.Errorf("wrong account password: HTTP %d, want 400", res.StatusCode)
	}
	if s.deps.AudioServer.Accounts.HasPassword(ctx, u.ID) {
		t.Fatal("audiobook password set without the account password")
	}
	if res := put(`{"password":"listen-long-1","current_password":"password123"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("right account password: HTTP %d", res.StatusCode)
	}
	if !s.deps.AudioServer.Accounts.HasPassword(ctx, u.ID) {
		t.Fatal("audiobook password not set")
	}

	// With two-factor on, a current code too.
	clock := &stepClock{t: time.Now().Truncate(30 * time.Second).Add(time.Second)}
	s.auth.SetClock(clock.now)
	setup, err := s.auth.BeginTOTP(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.auth.ConfirmTOTP(ctx, u.ID, totpAt(t, setup.Secret, clock.now())); err != nil {
		t.Fatal(err)
	}
	if rec := s.do("GET", "/api/v1/me/audio", c); !strings.Contains(rec.Body.String(), `"code_required":true`) {
		t.Errorf("GET /me/audio with two-factor on: %s", rec.Body)
	}
	if res := put(`{"password":"listen-long-2","current_password":"password123"}`); res.StatusCode != http.StatusBadRequest {
		t.Errorf("no code: HTTP %d, want 400", res.StatusCode)
	}
	code := totpAt(t, setup.Secret, clock.next())
	if res := put(`{"password":"listen-long-2","current_password":"password123","code":"` + code + `"}`); res.StatusCode != http.StatusOK {
		t.Errorf("password and code: HTTP %d", res.StatusCode)
	}

	// A too-short password is refused before anything is checked.
	if rec := s.doJSON("PUT", "/api/v1/me/audio/password", c, `{"password":"x"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "at least") {
		t.Errorf("short password: HTTP %d %s", rec.Code, rec.Body)
	}

	// A Plex-only account: its session alone within 15 minutes of signing in, then sign in again.
	p, err := s.auth.FindOrCreatePlexUser(ctx, "4242", "kidplex", auth.RoleRequester, auth.AutoApproval{})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _ := s.auth.CreateSession(ctx, p.ID)
	pc := &http.Cookie{Name: sessionCookieName, Value: tok}
	if rec := s.doJSON("PUT", "/api/v1/me/audio/password", pc, `{"password":"listen-long-3"}`); rec.Code != http.StatusOK {
		t.Errorf("Plex-only, fresh session: HTTP %d %s", rec.Code, rec.Body)
	}
	if _, err := s.st.DB().Exec(`UPDATE sessions SET created_at = datetime('now', '-1 hour') WHERE user_id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	rec := s.doJSON("PUT", "/api/v1/me/audio/password", pc, `{"password":"listen-long-4"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"reauth"`) {
		t.Errorf("Plex-only, an hour after signing in: HTTP %d %s, want 403 reauth", rec.Code, rec.Body)
	}
}
