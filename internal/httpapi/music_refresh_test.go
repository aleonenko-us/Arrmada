package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// optionsProvider is a MusicBrainz stand-in: one artist with a studio album and a live
// album (never the network).
type optionsProvider struct{}

func (optionsProvider) Available() bool { return true }
func (optionsProvider) SearchArtists(context.Context, string) ([]metadata.ArtistResult, error) {
	return nil, nil
}
func (optionsProvider) GetArtist(_ context.Context, mbid string) (*metadata.ArtistDetails, error) {
	return &metadata.ArtistDetails{ArtistResult: metadata.ArtistResult{MBID: mbid, Name: "Radiohead"}}, nil
}
func (optionsProvider) ArtistAlbums(context.Context, string) ([]metadata.AlbumResult, error) {
	return []metadata.AlbumResult{
		{MBID: "rg-okc", Title: "OK Computer", Type: "Album", PrimaryType: "Album", ReleaseDate: "1997-05-21", Year: 1997},
		{MBID: "rg-live", Title: "I Might Be Wrong", Type: "Live", PrimaryType: "Album", SecondaryTypes: []string{"Live"}, ReleaseDate: "2001-11-12", Year: 2001},
	}, nil
}
func (optionsProvider) AlbumTracks(context.Context, string) ([]metadata.TrackResult, error) {
	return nil, nil
}

// MUS-14's options round trip: an artist's options read and save (bad values refused),
// a release-type change lists the newly allowed album without wanting it, "Want matching
// albums" refuses a stale count and then applies; the library defaults round-trip; the
// New releases list answers; every route is 404 while Music is off.
func TestMusicOptionsRoundTrip(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	authSvc := auth.NewService(st.DB())
	u, err := authSvc.CreateUser(ctx, "manager", "correct-horse-battery", auth.RoleManager, true)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := authSvc.CreateSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	set := settings.NewService(st.DB())
	svc := music.NewService(st.DB(), optionsProvider{}, slog.Default())
	svc.SetSettingsLookup(set.Lookup)
	h := New(Deps{Log: slog.Default(), Store: st, Auth: authSvc, Settings: set, Music: svc, Quality: quality.NewService(st.DB())}).Handler

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/music/artists/1/options"}, {"PUT", "/api/v1/music/artists/1/options"},
		{"POST", "/api/v1/music/artists/1/options/apply"}, {"GET", "/api/v1/music/release-defaults"},
		{"PUT", "/api/v1/music/release-defaults"}, {"GET", "/api/v1/music/new-releases"},
	} {
		if rec := musicCallBody(h, c.method, c.path, session, `{}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s while off = %d, want 404", c.method, c.path, rec.Code)
		}
	}
	if err := set.SetBool(ctx, settings.KeyModuleMusic, true); err != nil {
		t.Fatal(err)
	}

	// Adding with bad choices is refused; a good add lists only the studio album.
	for _, body := range []string{
		`{"mbid":"mb-rh","monitor":"sometimes"}`,
		`{"mbid":"mb-rh","monitor_new":"maybe"}`,
		`{"mbid":"mb-rh","release_types":{"primary":["Bootleg"]}}`,
	} {
		if rec := musicCallBody(h, "POST", "/api/v1/music/artists", session, body); rec.Code != http.StatusBadRequest {
			t.Errorf("add %s = %d, want 400", body, rec.Code)
		}
	}
	rec := musicCallBody(h, "POST", "/api/v1/music/artists", session, `{"mbid":"mb-rh","monitor":"future"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	var art music.Artist
	_ = json.Unmarshal(rec.Body.Bytes(), &art)
	if len(art.Albums) != 1 || art.Albums[0].Monitored || art.MonitorNew != music.MonitorNewAll {
		t.Fatalf("added %+v, want one unwanted album and new releases wanted", art)
	}
	base := "/api/v1/music/artists/" + strconv.FormatInt(art.ID, 10) + "/options"

	type optionsBody struct {
		Options music.ArtistOptions `json:"options"`
		Choices map[string][]string `json:"choices"`
	}
	get := func() optionsBody {
		t.Helper()
		rec := musicCallBody(h, "GET", base, session, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET options = %d %s", rec.Code, rec.Body.String())
		}
		var b optionsBody
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := get()
	if b.Options.MonitorNew != "all" || b.Options.ReleaseTypes != nil || b.Options.EffectiveMonitorNew != "all" ||
		len(b.Options.EffectiveReleaseTypes.Primary) != 2 || len(b.Choices["secondary"]) == 0 || len(b.Options.Apply.Want) != 1 {
		t.Fatalf("options = %+v", b)
	}

	for _, body := range []string{`{"monitor_new":"sometimes"}`, `{"release_types":{"primary":[]}}`, `{"release_types":"Album"}`, `{"quality_profile":"nope"}`} {
		if rec := musicCallBody(h, "PUT", base, session, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", body, rec.Code)
		}
	}
	// Ticking Live lists the live album, not wanted, and the options say so.
	rec = musicCallBody(h, "PUT", base, session, `{"monitor_new":"none","release_types":{"primary":["Album"],"secondary":["Live"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT options = %d %s", rec.Code, rec.Body.String())
	}
	b = get()
	if b.Options.MonitorNew != "none" || b.Options.ReleaseTypes == nil || len(b.Options.ReleaseTypes.Secondary) != 1 {
		t.Errorf("after saving: %+v", b.Options)
	}
	if len(b.Options.Apply.Want) != 2 {
		t.Fatalf("apply plan wants %d, want the studio and the live album", len(b.Options.Apply.Want))
	}
	detail := musicCallBody(h, "GET", "/api/v1/music/artists/"+strconv.FormatInt(art.ID, 10), session, "")
	var withLive music.Artist
	_ = json.Unmarshal(detail.Body.Bytes(), &withLive)
	for _, al := range withLive.Albums {
		if al.Monitored {
			t.Errorf("%q became wanted by an options change", al.Title)
		}
	}
	if len(withLive.Albums) != 2 {
		t.Errorf("artist lists %d albums after ticking Live, want 2", len(withLive.Albums))
	}

	if rec := musicCallBody(h, "POST", base+"/apply", session, `{"want":1}`); rec.Code != http.StatusConflict {
		t.Errorf("apply with a stale count = %d, want 409", rec.Code)
	}
	if rec := musicCallBody(h, "POST", base+"/apply", session, `{"want":2}`); rec.Code != http.StatusOK {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body.String())
	}
	if b := get(); len(b.Options.Apply.Want) != 0 {
		t.Errorf("after applying the plan still wants %d", len(b.Options.Apply.Want))
	}
	// Back to the library default.
	if rec := musicCallBody(h, "PUT", base, session, `{"monitor_new":"","release_types":null}`); rec.Code != http.StatusOK {
		t.Fatalf("reset = %d", rec.Code)
	}
	if b := get(); b.Options.MonitorNew != "" || b.Options.ReleaseTypes != nil {
		t.Errorf("after reset: %+v", b.Options)
	}

	// The library defaults.
	for _, body := range []string{`{"monitor_new":"some"}`, `{"release_types":{"primary":["Album"],"secondary":["Bootleg"]}}`} {
		if rec := musicCallBody(h, "PUT", "/api/v1/music/release-defaults", session, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT defaults %s = %d, want 400", body, rec.Code)
		}
	}
	rec = musicCallBody(h, "PUT", "/api/v1/music/release-defaults", session, `{"monitor_new":"none","release_types":{"primary":["ep","Album"],"secondary":["live"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT defaults = %d %s", rec.Code, rec.Body.String())
	}
	if v, _ := set.Lookup(music.KeyReleaseTypes); v != `{"primary":["Album","EP"],"secondary":["Live"]}` {
		t.Errorf("stored types = %q, want MusicBrainz's spelling and order", v)
	}
	if b := get(); b.Options.EffectiveMonitorNew != "none" || b.Options.DefaultMonitorNew != "none" {
		t.Errorf("an artist on the default doesn't follow it: %+v", b.Options)
	}
	if rec := musicCallBody(h, "GET", "/api/v1/music/release-defaults", session, ""); rec.Code != http.StatusOK {
		t.Errorf("GET defaults = %d", rec.Code)
	}

	if rec := musicCallBody(h, "GET", "/api/v1/music/new-releases?days=0", session, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("days=0 = %d, want 400", rec.Code)
	}
	rec = musicCallBody(h, "GET", "/api/v1/music/new-releases", session, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("new releases = %d", rec.Code)
	}
	var nr struct {
		Albums []music.NewRelease `json:"albums"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &nr); err != nil || len(nr.Albums) != 1 || nr.Albums[0].FoundAs != music.FoundNotNew {
		t.Errorf("new releases = %+v (%v), want the live album listed as not new", nr.Albums, err)
	}
	if rec := musicCallBody(h, "GET", "/api/v1/music/artists/424242/options", session, ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown artist = %d, want 404", rec.Code)
	}
}
