package insights

import (
	"context"
	"errors"
	"testing"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
)

// One person's profile counts only their plays: totals as the People tab sums them,
// completion over plays with a known runtime, top titles by plays (a show by its name,
// with its poster), addresses grouped, and stalls from their own buffer events.
func TestPersonProfile(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	if err := s.repo.upsertUser(ctx, mediaserver.Plex, "7", "Sam", "https://plex.tv/users/abc/avatar?c=1", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.upsertUser(ctx, mediaserver.Plex, "8", "Kim", "", 100); err != nil {
		t.Fatal(err)
	}
	const base = int64(1_760_000_000)
	ins := func(r sessionRecord) int64 {
		t.Helper()
		id, err := s.repo.insertSession(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Sam: three Severance episodes (one finished), one film finished, an import with no runtime.
	for i := 0; i < 3; i++ {
		off := int64(1_000_000)
		if i == 0 {
			off = 3_300_000
		}
		ins(sessionRecord{UserID: "7", UserName: "Sam", RatingKey: "88" + string(rune('0'+i)), MediaType: "episode", Title: "Ep", GrandparentTitle: "Severance",
			Thumb: "/library/metadata/881/thumb/1", GrandparentThumb: "/library/metadata/800/thumb/1", GrandparentRatingKey: "800",
			Platform: "tvOS", Player: "Living Room", IPAddress: "192.168.1.31", Decision: "direct_play",
			StartedAt: base + int64(i)*3600, StoppedAt: base + int64(i)*3600 + 1800, DurationMS: 3_300_000, ViewOffsetMS: off})
	}
	film := ins(sessionRecord{UserID: "7", UserName: "Sam", RatingKey: "5", MediaType: "movie", Title: "Dune", Thumb: "/library/metadata/5/thumb/1",
		Platform: "Android", Player: "Phone", IPAddress: "203.0.113.9", Decision: "transcode", BufferCount: 2,
		StartedAt: base + 20000, StoppedAt: base + 27200, DurationMS: 9_000_000, ViewOffsetMS: 8_500_000})
	ins(sessionRecord{UserID: "7", UserName: "Sam", RatingKey: "6", MediaType: "movie", Title: "Heat", Platform: "Android", IPAddress: "203.0.113.9",
		StartedAt: base - 5000, StoppedAt: base - 1000, WatchedMS: 3_000_000})
	if err := s.repo.insertBufferEvent(ctx, film, base+21000, 0, 4000, "bandwidth", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.insertBufferEvent(ctx, film, base+22000, 0, 6000, "bandwidth", ""); err != nil {
		t.Fatal(err)
	}
	// Kim: a play and a stall of their own, which must not show on Sam's page.
	kim := ins(sessionRecord{UserID: "8", UserName: "Kim", RatingKey: "5", MediaType: "movie", Title: "Dune", IPAddress: "198.51.100.4",
		BufferCount: 1, StartedAt: base, StoppedAt: base + 600, DurationMS: 9_000_000, ViewOffsetMS: 100})
	if err := s.repo.insertBufferEvent(ctx, kim, base+100, 0, 50_000, "transcode", ""); err != nil {
		t.Fatal(err)
	}

	p, err := s.Person(ctx, "7", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Username != "Sam" || p.AvatarURL != "https://plex.tv/users/abc/avatar?c=1" {
		t.Errorf("identity = %q %q", p.Username, p.AvatarURL)
	}
	// Totals agree with the People tab's.
	users, err := s.Users(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.ID == "7" && (u.TotalPlays != p.TotalPlays || u.TotalSecs != p.TotalSecs) {
			t.Errorf("People tab %d plays / %d s, profile %d / %d", u.TotalPlays, u.TotalSecs, p.TotalPlays, p.TotalSecs)
		}
	}
	if p.TotalPlays != 5 || p.TotalSecs != 3*1800+7200+3000 {
		t.Errorf("totals = %d plays %d s", p.TotalPlays, p.TotalSecs)
	}
	// 2 of the 4 plays with a runtime reached 90% (the import has none).
	if p.CompletionOf != 4 || p.CompletionPct != 50 {
		t.Errorf("completion = %v%% of %d", p.CompletionPct, p.CompletionOf)
	}
	if len(p.TopTitles) != 3 || p.TopTitles[0].Title != "Severance" || p.TopTitles[0].Plays != 3 || imagePathOf(t, p.TopTitles[0].ThumbURL) != "/library/metadata/800/thumb/1" {
		t.Errorf("top titles = %+v", p.TopTitles)
	}
	if len(p.IPs) != 2 || p.IPs[0].IP != "203.0.113.9" || p.IPs[0].Plays != 2 || p.IPs[1].Plays != 3 {
		t.Errorf("ips = %+v", p.IPs)
	}
	if len(p.Platforms) != 2 || p.Platforms[0].Name != "tvOS" || p.Platforms[0].Plays != 3 {
		t.Errorf("platforms = %+v", p.Platforms)
	}
	if p.Decisions != (PersonDecisions{DirectPlay: 4, Transcode: 1}) {
		t.Errorf("decisions = %+v", p.Decisions)
	}
	if p.Stalls.Events != 2 || p.Stalls.StallMS != 10_000 || p.Stalls.RatePct != 20 {
		t.Errorf("stalls = %+v (Kim's must not count)", p.Stalls)
	}
	if len(p.Recent) != 5 || p.Recent[0].Title != "Dune" {
		t.Errorf("recent = %d rows, first %q", len(p.Recent), p.Recent[0].Title)
	}
	for _, r := range p.Recent {
		if r.UserID != "7" {
			t.Errorf("someone else's play in Sam's recent: %+v", r)
		}
	}

	// A window keeps only the plays inside it (all of Sam's are long past here).
	w, err := s.Person(ctx, "7", 7)
	if err != nil || w.TotalPlays != 0 || w.Username != "Sam" {
		t.Errorf("windowed = %d plays (%v)", w.TotalPlays, err)
	}

	if _, err := s.Person(ctx, "999", 0); !errors.Is(err, ErrPersonNotFound) {
		t.Errorf("unknown person: %v", err)
	}
}

// Only plex.tv avatars reach the browser.
func TestAvatarURLAllowlist(t *testing.T) {
	for in, want := range map[string]string{
		"https://plex.tv/users/abc/avatar?c=1": "https://plex.tv/users/abc/avatar?c=1",
		"http://plex.tv/users/abc/avatar":      "",
		"https://plex.tv.evil.com/a.png":       "",
		"https://evil.com/https://plex.tv/":    "",
		"/library/metadata/1/thumb":            "",
		"javascript:alert(1)":                  "",
		"":                                     "",
	} {
		if got := avatarURL(in); got != want {
			t.Errorf("avatarURL(%q) = %q, want %q", in, got, want)
		}
	}
}
