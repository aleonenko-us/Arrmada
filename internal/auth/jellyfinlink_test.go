package auth

import (
	"context"
	"errors"
	"testing"
)

// Jellyfin sign-in makes a passwordless requester named after the Jellyfin user, and finds
// the same account next time; a name already taken gets a suffix.
func TestFindOrCreateJellyfinUser(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if _, err := s.CreateUser(ctx, "mum", "supersecret", RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	u, err := s.FindOrCreateJellyfinUser(ctx, "jf-1", "mum", RoleRequester, AutoApproval{Movie: true})
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "mum-2" || u.Role != RoleRequester || !u.JellyfinLinked || u.JellyfinUsername != "mum" || !u.JellyfinOnly || !u.AutoApproveMovie || u.AutoApproveSeries {
		t.Errorf("new account = %+v", u)
	}
	if u.PlexLinked || u.PlexOnly {
		t.Errorf("a Jellyfin account says it has Plex: %+v", u)
	}
	again, err := s.FindOrCreateJellyfinUser(ctx, "jf-1", "Mum", RoleAdmin, AutoApproval{})
	if err != nil || again.ID != u.ID || again.Role != RoleRequester {
		t.Errorf("second sign-in = %+v, %v; want the same requester", again, err)
	}
	if l, _ := s.JellyfinLinkFor(ctx, u.ID); !l.Linked || l.JellyfinUsername != "Mum" || l.CanUnlink {
		t.Errorf("link = %+v (name kept current, can't unlink its only way in)", l)
	}
	if got, _ := s.UserByJellyfinID(ctx, "jf-1"); got == nil || got.ID != u.ID {
		t.Errorf("UserByJellyfinID = %+v", got)
	}
	if got, err := s.UserByJellyfinID(ctx, "nobody"); got != nil || err != nil {
		t.Errorf("unknown id = %+v, %v", got, err)
	}
	if got, err := s.UserByJellyfinID(ctx, ""); got != nil || err != nil {
		t.Errorf("blank id = %+v, %v", got, err)
	}
	if _, err := s.FindOrCreateJellyfinUser(ctx, " ", "x", RoleRequester, AutoApproval{}); err == nil {
		t.Error("a blank Jellyfin id made an account")
	}
}

// One Jellyfin user is one account: linking it to a second is refused and names the holder.
func TestLinkJellyfinConflict(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	kid, _ := s.FindOrCreateJellyfinUser(ctx, "jf-kid", "kid", RoleRequester, AutoApproval{})
	mum, _ := s.CreateUser(ctx, "mum@example.com", "supersecret", RoleRequester, false)
	err := s.LinkJellyfin(ctx, mum.ID, "jf-kid", "kid")
	var taken *ErrJellyfinAlreadyLinked
	if !errors.As(err, &taken) || taken.UserID != kid.ID || taken.Username != "kid" {
		t.Fatalf("err = %v, want ErrJellyfinAlreadyLinked naming kid", err)
	}
	if s.JellyfinIDForUser(ctx, mum.ID) != "" {
		t.Error("a refused link changed mum")
	}
	if err := s.LinkJellyfin(ctx, mum.ID, "jf-mum", "mum"); err != nil || s.JellyfinIDForUser(ctx, mum.ID) != "jf-mum" {
		t.Errorf("link = %v, id %q", err, s.JellyfinIDForUser(ctx, mum.ID))
	}
	// The same Jellyfin id and a Plex id with the same text don't collide.
	if err := s.LinkPlex(ctx, mum.ID, "jf-kid", "x"); err != nil {
		t.Errorf("plex id equal to a jellyfin id: %v", err)
	}
}

// An account nobody knows a password for can drop one outside link while it has the other,
// never the last one; an admin (checkLogin false) can.
func TestUnlinkLastExternalRefused(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	u, _ := s.FindOrCreateJellyfinUser(ctx, "jf-1", "kid", RoleRequester, AutoApproval{})
	if err := s.UnlinkJellyfin(ctx, u.ID, true); !errors.Is(err, ErrJellyfinOnlyLogin) {
		t.Fatalf("unlink the only way in: %v, want ErrJellyfinOnlyLogin", err)
	}
	if err := s.LinkPlex(ctx, u.ID, "plex-1", "kidplex"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.UserByID(ctx, u.ID)
	if got.JellyfinOnly || got.PlexOnly {
		t.Errorf("with both linked neither is the only way in: %+v", got)
	}
	if l, _ := s.JellyfinLinkFor(ctx, u.ID); !l.CanUnlink {
		t.Error("with Plex linked too, Jellyfin should be unlinkable")
	}
	if err := s.UnlinkJellyfin(ctx, u.ID, true); err != nil {
		t.Fatalf("unlink Jellyfin while Plex is linked: %v", err)
	}
	if err := s.UnlinkPlex(ctx, u.ID, true); !errors.Is(err, ErrPlexOnlyLogin) {
		t.Errorf("unlink the last link (Plex): %v, want ErrPlexOnlyLogin", err)
	}
	if got, _ := s.UserByID(ctx, u.ID); !got.PlexOnly {
		t.Errorf("Plex is now the only way in: %+v", got)
	}
	if err := s.UnlinkPlex(ctx, u.ID, false); err != nil {
		t.Errorf("admin unlink: %v", err)
	}
	if err := s.UnlinkJellyfin(ctx, 9999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

// A duplicate holding both links hands both to an account with neither.
func TestMergeDuplicateMovesBothLinks(t *testing.T) {
	ctx := context.Background()
	f := newMergeFixture(t)
	if err := f.s.LinkJellyfin(ctx, f.dup.ID, "jf-dup", "ownerjf"); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.MergePreview(ctx, f.admin.ID, f.dup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.PlexLink || !p.JellyfinLink || p.JellyfinUsername != "ownerjf" || p.PlexUsername != "ownerplex" {
		t.Errorf("preview = %+v", p)
	}
	if err := f.s.MergeDuplicate(ctx, f.admin.ID, f.dup.ID); err != nil {
		t.Fatal(err)
	}
	if f.s.PlexIDForUser(ctx, f.admin.ID) != "4242" || f.s.JellyfinIDForUser(ctx, f.admin.ID) != "jf-dup" {
		t.Errorf("links after merge: plex %q jellyfin %q", f.s.PlexIDForUser(ctx, f.admin.ID), f.s.JellyfinIDForUser(ctx, f.admin.ID))
	}
	if l, _ := f.s.JellyfinLinkFor(ctx, f.admin.ID); l.JellyfinUsername != "ownerjf" {
		t.Errorf("jellyfin link = %+v", l)
	}
	if _, err := f.s.UserByID(ctx, f.dup.ID); err == nil {
		t.Error("the duplicate is still there")
	}
}

// A Jellyfin-only duplicate merges into an account that has Plex but no Jellyfin; one whose
// Jellyfin link would collide with the target's own is refused, and nothing moves.
func TestMergeDuplicateJellyfinRules(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	owner, _ := s.CreateUser(ctx, "owner", "supersecret", RoleAdmin, false)
	_ = s.LinkPlex(ctx, owner.ID, "plex-owner", "ownerplex")
	dup, _ := s.FindOrCreateJellyfinUser(ctx, "jf-owner", "ownerjf", RoleRequester, AutoApproval{})
	if p, err := s.MergePreview(ctx, owner.ID, dup.ID); err != nil || p.PlexLink || !p.JellyfinLink {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	if err := s.MergeDuplicate(ctx, owner.ID, dup.ID); err != nil {
		t.Fatal(err)
	}
	if s.PlexIDForUser(ctx, owner.ID) != "plex-owner" || s.JellyfinIDForUser(ctx, owner.ID) != "jf-owner" {
		t.Error("the owner's Plex link changed or the Jellyfin link didn't arrive")
	}

	other, _ := s.FindOrCreateJellyfinUser(ctx, "jf-other", "other", RoleRequester, AutoApproval{})
	var refused *ErrMergeRefused
	if err := s.MergeDuplicate(ctx, owner.ID, other.ID); !errors.As(err, &refused) {
		t.Fatalf("merging a second Jellyfin user in: %v, want a refusal", err)
	}
	if s.JellyfinIDForUser(ctx, other.ID) != "jf-other" || s.JellyfinIDForUser(ctx, owner.ID) != "jf-owner" {
		t.Error("a refused merge moved a link")
	}
}
