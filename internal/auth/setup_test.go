package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// A setup session is valid until it expires, and the moment an admin exists it is dead,
// whichever way the admin came to be.
func TestSetupSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	now := time.Now()
	s.SetClock(func() time.Time { return now })

	tok, exp, err := s.CreateSetupSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(SetupSessionTTL); exp.Sub(want).Abs() > time.Second {
		t.Errorf("expires %v, want about %v", exp, want)
	}
	if !s.SetupSessionValid(ctx, tok) {
		t.Fatal("a fresh setup session isn't valid")
	}
	if s.SetupSessionValid(ctx, "") || s.SetupSessionValid(ctx, tok+"x") || s.SetupSessionValid(ctx, "nope") {
		t.Error("a wrong or empty token was accepted")
	}
	// The token itself is never stored.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM setup_sessions WHERE token_hash = ?`, tok).Scan(&n); err != nil || n != 0 {
		t.Errorf("the raw token is stored (rows=%d err=%v)", n, err)
	}
	// A requester doesn't end setup; only an admin does.
	if _, err := s.CreateUser(ctx, "kid@example.com", "password1", RoleRequester, false); err != nil {
		t.Fatal(err)
	}
	if !s.SetupSessionValid(ctx, tok) {
		t.Error("a requester account killed the setup session")
	}
	// Expiry is time alone.
	now = now.Add(SetupSessionTTL - time.Minute)
	if !s.SetupSessionValid(ctx, tok) {
		t.Error("expired a minute early")
	}
	now = now.Add(2 * time.Minute)
	if s.SetupSessionValid(ctx, tok) {
		t.Error("a setup session outlived its two hours")
	}

	// An admin made any other way kills a live one on the very next check.
	now = time.Now()
	tok2, _, err := s.CreateSetupSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.SetupSessionValid(ctx, tok2) {
		t.Fatal("fresh session invalid")
	}
	if _, err := s.CreateUser(ctx, "boss@example.com", "password1", RoleAdmin, true); err != nil {
		t.Fatal(err)
	}
	if s.SetupSessionValid(ctx, tok2) {
		t.Error("a setup session survived an admin existing (checked from the table, not issue time)")
	}
	// And none is ever issued once an admin exists.
	if _, _, err := s.CreateSetupSession(ctx); !errors.Is(err, ErrSetupClosed) {
		t.Errorf("issued with an admin present: err = %v, want ErrSetupClosed", err)
	}
}

// Two browsers finishing at once make one admin: the insert is conditional in SQL.
func TestCreateFirstAdminRace(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	tok, _, _ := s.CreateSetupSession(ctx)

	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = s.CreateFirstAdmin(ctx, "owner"+string(rune('a'+i))+"@example.com", "password1")
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrSetupClosed), errors.Is(err, ErrUserExists):
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Errorf("%d callers made an admin, want exactly 1", won)
	}
	if n, _ := s.CountAdmins(ctx); n != 1 {
		t.Errorf("%d admins, want 1", n)
	}
	if s.SetupSessionValid(ctx, tok) {
		t.Error("the setup session survived the admin")
	}
	var rows int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM setup_sessions`).Scan(&rows)
	if rows != 0 {
		t.Errorf("%d setup sessions left in the table after the admin was made", rows)
	}
	if _, err := s.CreateFirstAdmin(ctx, "late@example.com", "password1"); !errors.Is(err, ErrSetupClosed) {
		t.Errorf("a late first admin: err = %v, want ErrSetupClosed", err)
	}
	if _, err := s.CreateFirstAdmin(ctx, "", "password1"); !errors.Is(err, ErrUsernameRequired) {
		t.Errorf("blank name: err = %v", err)
	}
}

// The owner claim makes a passwordless admin linked to the media-server user, once, and
// never promotes someone who already has an account.
func TestClaimFirstAdminViaLink(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	req, err := s.FindOrCreatePlexUser(ctx, "42", "friend", RoleRequester, AutoApproval{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimFirstAdminViaLink(ctx, "plex", "42", "friend"); !errors.Is(err, ErrUserExists) {
		t.Errorf("claiming a linked requester's Plex id: err = %v, want ErrUserExists", err)
	}
	if u, _ := s.UserByID(ctx, req.ID); u.Role != RoleRequester {
		t.Errorf("the requester was promoted to %s", u.Role)
	}
	u, err := s.ClaimFirstAdminViaLink(ctx, "plex", "1", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleAdmin || !u.PlexLinked || !u.PlexOnly {
		t.Errorf("claimed = %+v, want a Plex-only admin", u)
	}
	if _, err := s.ClaimFirstAdminViaLink(ctx, "jellyfin", "jf-2", "other"); !errors.Is(err, ErrSetupClosed) {
		t.Errorf("a second claim: err = %v, want ErrSetupClosed", err)
	}
	if _, err := s.ClaimFirstAdminViaLink(ctx, "emby", "x", "y"); err == nil {
		t.Error("an unknown media server was accepted")
	}
}

// Issuing can't grow the table without bound: the oldest goes first.
func TestSetupSessionsAreCapped(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	first, _, _ := s.CreateSetupSession(ctx)
	for i := 0; i < setupSessionMax+5; i++ {
		if _, _, err := s.CreateSetupSession(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM setup_sessions`).Scan(&n)
	if n > setupSessionMax {
		t.Errorf("%d live setup sessions, cap is %d", n, setupSessionMax)
	}
	if s.SetupSessionValid(ctx, first) {
		t.Error("the oldest setup session survived the cap")
	}
}
