package requests

import (
	"context"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/quality"
)

// movieProfile adds a custom movie profile and returns its ref.
func movieProfile(t *testing.T, s *Service, name string) string {
	t.Helper()
	sp, err := s.quality.Create(context.Background(), quality.StoredProfile{MediaType: quality.MediaMovie, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return "custom:" + strconv.FormatInt(sp.ID, 10)
}

// Staff asking for a film from Discover with a profile picked: their auto-approve adds it
// on that profile, not the default.
func TestAutoApprovedRequestAddsWithItsProfile(t *testing.T) {
	s, _, _ := quietFixture(t)
	ctx := context.Background()
	hd720 := movieProfile(t, s, "HD-720p")

	got, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 603, Title: "The Matrix", QualityProfile: hd720, RequestedBy: 1, RequestedByName: "admin"},
		CreateOptions{AutoApprove: true, QuotaExempt: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusApproved || got.QualityProfile != hd720 {
		t.Fatalf("request = status %q profile %q, want approved on %q", got.Status, got.QualityProfile, hd720)
	}
	m, err := s.movies.GetByTMDB(ctx, 603)
	if err != nil {
		t.Fatal(err)
	}
	if m.QualityProfile != hd720 {
		t.Errorf("movie added on %q, want %q", m.QualityProfile, hd720)
	}
}

// Staff naming a profile for a film someone already asked for: the pending request takes
// their pick (the approve sheet starts on it). An approved one was added on its own profile
// and keeps it, and a later ask with no profile changes nothing.
func TestStaffProfileOnExistingRequest(t *testing.T) {
	s, _, _ := quietFixture(t)
	ctx := context.Background()
	hd720 := movieProfile(t, s, "HD-720p")
	uhd := movieProfile(t, s, "Ultra HD")

	pending, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 604, Title: "Heat", RequestedBy: 7, RequestedByName: "alice"}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, subscribed, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 604, Title: "Heat", QualityProfile: hd720, RequestedBy: 1, RequestedByName: "admin"},
		CreateOptions{QuotaExempt: true})
	if err != nil || !subscribed {
		t.Fatalf("staff ask: subscribed=%v err=%v", subscribed, err)
	}
	if got.ID != pending.ID || got.QualityProfile != hd720 {
		t.Fatalf("pending request = id %d profile %q, want %d on %q", got.ID, got.QualityProfile, pending.ID, hd720)
	}
	// Someone else following it later, with no profile, leaves staff's pick alone.
	if _, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 604, Title: "Heat", RequestedBy: 8, RequestedByName: "bob"}, CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := s.repo.Get(ctx, pending.ID); fresh.QualityProfile != hd720 {
		t.Errorf("after a plain follow the profile is %q, want %q", fresh.QualityProfile, hd720)
	}

	approved, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 605, Title: "Ronin", QualityProfile: hd720, RequestedBy: 1}, CreateOptions{AutoApprove: true, QuotaExempt: true})
	if err != nil || approved.Status != StatusApproved {
		t.Fatalf("approved create: %+v %v", approved, err)
	}
	if _, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 605, Title: "Ronin", QualityProfile: uhd, RequestedBy: 2}, CreateOptions{QuotaExempt: true}); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := s.repo.Get(ctx, approved.ID); fresh.QualityProfile != hd720 {
		t.Errorf("approved request's profile became %q, want it kept at %q", fresh.QualityProfile, hd720)
	}
	if m, _ := s.movies.GetByTMDB(ctx, 605); m.QualityProfile != hd720 {
		t.Errorf("the library film's profile became %q, want %q", m.QualityProfile, hd720)
	}
}

// A declined film asked for again by staff with a profile re-opens on their pick; asked
// for again without one, it keeps the profile it had.
func TestReopenedRequestProfile(t *testing.T) {
	s, _, _ := quietFixture(t)
	ctx := context.Background()
	hd720 := movieProfile(t, s, "HD-720p")

	req, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 606, Title: "Thief", RequestedBy: 7}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Decline(ctx, req.ID, DeclineOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 606, Title: "Thief", Note: "worth it", QualityProfile: hd720, RequestedBy: 1}, CreateOptions{QuotaExempt: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending || got.QualityProfile != hd720 {
		t.Fatalf("re-opened = status %q profile %q, want pending on %q", got.Status, got.QualityProfile, hd720)
	}
	if err := s.Decline(ctx, req.ID, DeclineOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Create(ctx, Request{MediaType: "movie", TMDBID: 606, Title: "Thief", Note: "please", RequestedBy: 7}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.QualityProfile != hd720 {
		t.Errorf("re-opened without a profile: %q, want it kept at %q", got.QualityProfile, hd720)
	}
}

// Staff asking for a show whose seasons are all waiting on someone else's request follow
// it, and that pending request takes their profile.
func TestStaffProfileOnFollowedSeriesRequest(t *testing.T) {
	s, _, _ := quietFixture(t)
	ctx := context.Background()
	sp, err := s.quality.Create(ctx, quality.StoredProfile{MediaType: quality.MediaSeries, Name: "HD-720p"})
	if err != nil {
		t.Fatal(err)
	}
	hd720 := "custom:" + strconv.FormatInt(sp.ID, 10)

	pending, _, err := s.Create(ctx, Request{MediaType: "series", TMDBID: 77, Title: "Show", RequestedBy: 7}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, subscribed, err := s.Create(ctx, Request{MediaType: "series", TMDBID: 77, Title: "Show", QualityProfile: hd720, RequestedBy: 1}, CreateOptions{QuotaExempt: true})
	if err != nil || !subscribed {
		t.Fatalf("staff ask: subscribed=%v err=%v", subscribed, err)
	}
	if got.ID != pending.ID || got.QualityProfile != hd720 {
		t.Errorf("followed request = id %d profile %q, want %d on %q", got.ID, got.QualityProfile, pending.ID, hd720)
	}
}
