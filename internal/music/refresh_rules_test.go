package music

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// catalogProvider is a MusicBrainz stand-in for the refresh rules: an artist's release
// groups (albums and EPs, and the "other" browse), release-group lookups that can answer
// under a merged id or not at all, and per-group listings.
type catalogProvider struct {
	mu       sync.Mutex
	artistID map[string]string // asked → answered under (an artist merge)
	main     map[string][]metadata.AlbumResult
	other    map[string][]metadata.AlbumResult
	merged   map[string]string // release group → the group it was merged into
	removed  map[string]bool   // release groups MusicBrainz no longer has
	tracks   map[string][]metadata.TrackResult
	down     bool
	calls    map[string]int
	notFound error
}

func newCatalog() *catalogProvider {
	return &catalogProvider{
		artistID: map[string]string{}, main: map[string][]metadata.AlbumResult{}, other: map[string][]metadata.AlbumResult{},
		merged: map[string]string{}, removed: map[string]bool{}, tracks: map[string][]metadata.TrackResult{}, calls: map[string]int{},
	}
}

var errDown = fmt.Errorf("%w: 503", metadata.ErrMusicBrainzUnavailable)

func (p *catalogProvider) count(k string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[k]++
	return p.down
}

func (p *catalogProvider) Available() bool { return true }
func (p *catalogProvider) SearchArtists(context.Context, string) ([]metadata.ArtistResult, error) {
	return nil, nil
}
func (p *catalogProvider) GetArtist(_ context.Context, mbid string) (*metadata.ArtistDetails, error) {
	if p.count("artist") {
		return nil, errDown
	}
	id := mbid
	if to, ok := p.artistID[mbid]; ok {
		id = to
	}
	return &metadata.ArtistDetails{ArtistResult: metadata.ArtistResult{MBID: id, Name: "Radiohead"}}, nil
}
func (p *catalogProvider) ArtistAlbums(_ context.Context, mbid string) ([]metadata.AlbumResult, error) {
	if p.count("main") {
		return nil, errDown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]metadata.AlbumResult(nil), p.main[mbid]...), nil
}
func (p *catalogProvider) ArtistOtherReleaseGroups(_ context.Context, mbid string) ([]metadata.AlbumResult, error) {
	if p.count("other") {
		return nil, errDown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]metadata.AlbumResult(nil), p.other[mbid]...), nil
}
func (p *catalogProvider) ReleaseGroup(_ context.Context, mbid string) (metadata.AlbumResult, error) {
	if p.count("rg") {
		return metadata.AlbumResult{}, errDown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if to, ok := p.merged[mbid]; ok {
		return metadata.AlbumResult{MBID: to, Title: "merged"}, nil
	}
	if p.removed[mbid] {
		return metadata.AlbumResult{}, p.notFound
	}
	return metadata.AlbumResult{MBID: mbid}, nil
}
func (p *catalogProvider) AlbumTracks(_ context.Context, rg string) ([]metadata.TrackResult, error) {
	if p.count("tracks") {
		return nil, errDown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tracks[rg], nil
}

// realNotFound is the client's own "no such thing", made by asking a stub that answers
// 404, so IsNotFound sees exactly what production code would.
func realNotFound(t *testing.T) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not Found"}`))
	}))
	defer srv.Close()
	m := metadata.NewMusicBrainz(nil, func() metadata.MBConfig { return metadata.MBConfig{BaseURL: srv.URL, MirrorRate: 50} })
	_, err := m.ReleaseGroup(context.Background(), "gone")
	if !metadata.IsNotFound(err) {
		t.Fatalf("stub lookup = %v, want not found", err)
	}
	return err
}

func rg(id, title, primary, date string, secondary ...string) metadata.AlbumResult {
	label := primary
	if len(secondary) > 0 {
		label = secondary[0]
	}
	return metadata.AlbumResult{MBID: id, Title: title, Type: label, PrimaryType: primary, SecondaryTypes: secondary,
		ReleaseDate: date, Year: releaseDay(date).Year()}
}

// refreshFixture is a service over a fresh store and the catalog, its clock at
// 2026-10-20.
type refreshFixture struct {
	svc  *Service
	repo *Repo
	p    *catalogProvider
	ctx  context.Context
	now  time.Time
	sets map[string]string
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &refreshFixture{p: newCatalog(), ctx: context.Background(), now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), sets: map[string]string{}}
	f.svc = NewService(st.DB(), f.p, slog.Default())
	f.svc.now = func() time.Time { return f.now }
	f.svc.SetSettingsLookup(func(k string) (string, bool) { v, ok := f.sets[k]; return v, ok })
	f.repo = f.svc.repo
	return f
}

func (f *refreshFixture) album(t *testing.T, mbid string) Album {
	t.Helper()
	id, ok, err := f.repo.AlbumIDByMBID(f.ctx, mbid)
	if err != nil || !ok {
		t.Fatalf("no album %s (%v)", mbid, err)
	}
	al, err := f.repo.GetAlbum(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return al
}

func (f *refreshFixture) hasAlbum(mbid string) bool {
	_, ok, _ := f.repo.AlbumIDByMBID(f.ctx, mbid)
	return ok
}

func (f *refreshFixture) albumCount(t *testing.T, artistID int64) int {
	t.Helper()
	als, err := f.repo.AlbumsFor(f.ctx, artistID)
	if err != nil {
		t.Fatal(err)
	}
	return len(als)
}

// A refresh adds a release MusicBrainz newly lists and wants it per the artist's
// new-release choice; an album the owner unmonitored stays unmonitored.
func TestRefreshFindsNewReleaseAndMonitorsPerMonitorNew(t *testing.T) {
	for _, tc := range []struct {
		name          string
		monitorNew    string
		artistOn      bool
		wantMonitored bool
	}{
		{"all", MonitorNewAll, true, true},
		{"none", MonitorNewNone, true, false},
		{"library default (all)", "", true, true},
		{"artist not monitored", MonitorNewAll, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefreshFixture(t)
			f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-okc", "OK Computer", "Album", "1997-05-21"), rg("rg-kida", "Kid A", "Album", "2000-10-02")}
			a, err := f.svc.AddArtistWith(f.ctx, "mb-rh", "", AddOptions{Monitored: true, Monitor: MonitorAll, MonitorNew: tc.monitorNew})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.svc.SetAlbumMonitored(f.ctx, f.album(t, "rg-kida").ID, false); err != nil {
				t.Fatal(err)
			}
			if !tc.artistOn {
				if _, err := f.repo.db.Exec(`UPDATE artists SET monitored = 0 WHERE id = ?`, a.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.p.main["mb-rh"] = append(f.p.main["mb-rh"], rg("rg-new", "A Moon Shaped Pool II", "Album", "2026-10-03"))
			res, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Added) != 1 {
				t.Fatalf("added %d albums, want the one new release", len(res.Added))
			}
			al := f.album(t, "rg-new")
			if al.Monitored != tc.wantMonitored || al.FoundAs != FoundNew || al.FoundAt == "" {
				t.Errorf("new release monitored=%v found=%q at %q; want monitored=%v, new", al.Monitored, al.FoundAs, al.FoundAt, tc.wantMonitored)
			}
			if f.album(t, "rg-kida").Monitored {
				t.Error("the album the owner unmonitored is monitored again")
			}
			if !f.album(t, "rg-okc").Monitored && tc.artistOn {
				t.Error("an existing wanted album lost its monitoring")
			}
			got, _ := f.repo.GetArtist(f.ctx, a.ID)
			if got.LastRefreshedAt == "" {
				t.Error("last_refreshed_at not stamped")
			}
			if tc.wantMonitored {
				evs, _ := f.repo.Events(f.ctx, a.ID, 20)
				found := false
				for _, e := range evs {
					found = found || strings.HasPrefix(e.Detail, "New release: A Moon Shaped Pool II (Album, 2026-10-03)")
				}
				if !found {
					t.Errorf("no new-release event in %+v", evs)
				}
			}
		})
	}
}

// Old music MusicBrainz only now lists, too many "new" releases at once, and anything a
// change of release types brings in are listed, never wanted on their own.
func TestRefreshNeverSurpriseGrabs(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{
		rg("rg-okc", "OK Computer", "Album", "1997-05-21"),
		rg("rg-live", "I Might Be Wrong", "Album", "2001-11-12", "Live"),
	}
	a, err := f.svc.AddArtist(f.ctx, "mb-rh", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if f.hasAlbum("rg-live") {
		t.Fatal("a live album was listed with the default types (Album, EP; no secondary types)")
	}

	// 1. Old music newly catalogued: listed, not wanted.
	f.p.main["mb-rh"] = append(f.p.main["mb-rh"], rg("rg-old", "Drill", "EP", "1992-05-05"))
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-old"); al.Monitored || al.FoundAs != FoundBackCatalogue {
		t.Errorf("back catalogue: monitored=%v found=%q", al.Monitored, al.FoundAs)
	}

	// 2. Live switched on: the live album MusicBrainz always listed comes in, not wanted.
	live := ReleaseTypes{Primary: []string{"Album", "EP"}, Secondary: []string{"Live"}}
	changed, err := f.svc.SetOptions(f.ctx, a.ID, "", &live)
	if err != nil || !changed {
		t.Fatalf("SetOptions: changed=%v err=%v", changed, err)
	}
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-live"); al.Monitored || al.FoundAs != FoundNotNew {
		t.Errorf("after ticking Live: monitored=%v found=%q, want listed as not new", al.Monitored, al.FoundAs)
	}
	// Singles ticked later: even a recent one was on MusicBrainz before anyone asked, so
	// it's listed, not wanted (the singles browse is new to this artist, not the single).
	withSingles := ReleaseTypes{Primary: []string{"Album", "EP", "Single"}, Secondary: []string{"Live"}}
	if _, err := f.svc.SetOptions(f.ctx, a.ID, "", &withSingles); err != nil {
		t.Fatal(err)
	}
	f.p.other["mb-rh"] = []metadata.AlbumResult{rg("rg-recent-single", "Recent Single", "Single", "2026-10-01")}
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-recent-single"); al.Monitored || al.FoundAs != FoundNotNew {
		t.Errorf("a single brought in by ticking Single: monitored=%v found=%q, want listed as not new", al.Monitored, al.FoundAs)
	}
	// The refresh that follows an options change lists without wanting, even a release
	// that happens to be new.
	f.p.main["mb-rh"] = append(f.p.main["mb-rh"], rg("rg-same-day", "Same Day", "Album", "2026-10-15"))
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true, ListOnly: true}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-same-day"); al.Monitored || al.FoundAs != FoundNotNew {
		t.Errorf("list-only refresh: monitored=%v found=%q", al.Monitored, al.FoundAs)
	}
	back := ReleaseTypes{Primary: []string{"Album", "EP"}, Secondary: []string{"Live"}}
	if _, err := f.svc.SetOptions(f.ctx, a.ID, "", &back); err != nil {
		t.Fatal(err)
	}

	// "Want matching albums" shows the count first, refuses a stale count, then applies.
	plan, err := f.svc.ApplyPreview(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Want) != 3 { // the live album, the back-catalogue EP and the list-only album
		t.Fatalf("preview wants %d albums (%+v), want 3", len(plan.Want), plan.Want)
	}
	if _, err := f.svc.ApplyOptions(f.ctx, a.ID, 5, 0, 0); !errors.Is(err, ErrPlanChanged) {
		t.Errorf("apply with a stale count = %v, want ErrPlanChanged", err)
	}
	if f.album(t, "rg-live").Monitored {
		t.Fatal("a refused apply changed something")
	}
	if _, err := f.svc.ApplyOptions(f.ctx, a.ID, 3, 0, 0); err != nil {
		t.Fatal(err)
	}
	if !f.album(t, "rg-live").Monitored || !f.album(t, "rg-old").Monitored {
		t.Error("apply didn't want the matching albums")
	}

	// 3. A flood of new releases at once is a catalogue change: listed, none wanted.
	for i := 0; i < maxNewPerRefresh+2; i++ {
		f.p.main["mb-rh"] = append(f.p.main["mb-rh"], rg(fmt.Sprintf("rg-flood-%d", i), fmt.Sprintf("Flood %d", i), "Album", "2026-09-01"))
	}
	res, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Wanted) != 0 || f.album(t, "rg-flood-0").FoundAs != FoundTooMany {
		t.Errorf("flood: %d wanted, found=%q; want none wanted, too_many", len(res.Wanted), f.album(t, "rg-flood-0").FoundAs)
	}
}

// Narrowing the types never unmonitors an album with files; the apply plan only offers to
// stop wanting ones that hold nothing.
func TestApplyPlanNeverUnwantsAlbumsWithFiles(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-ep", "Airbag EP", "EP", "1998-03-01"), rg("rg-ep2", "No Surprises EP", "EP", "1998-01-12")}
	a, err := f.svc.AddArtist(f.ctx, "mb-rh", "", true)
	if err != nil {
		t.Fatal(err)
	}
	held := f.album(t, "rg-ep")
	if err := f.repo.UpsertTracks(f.ctx, held.ID, []Track{{DiscNumber: 1, TrackNumber: 1, Title: "Airbag"}}); err != nil {
		t.Fatal(err)
	}
	tracks, _ := f.repo.TracksFor(f.ctx, held.ID)
	if err := f.repo.SetTrackFile(f.ctx, tracks[0].ID, "/m/airbag.flac", "FLAC", 900, 10, ""); err != nil {
		t.Fatal(err)
	}
	albumsOnly := ReleaseTypes{Primary: []string{"Album"}}
	if _, err := f.svc.SetOptions(f.ctx, a.ID, "", &albumsOnly); err != nil {
		t.Fatal(err)
	}
	if !f.album(t, "rg-ep").Monitored || !f.album(t, "rg-ep2").Monitored {
		t.Fatal("changing the types changed monitoring by itself")
	}
	plan, err := f.svc.ApplyPreview(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unwant) != 1 || plan.Unwant[0].Title != "No Surprises EP" {
		t.Errorf("unwant = %+v, want only the EP with no files", plan.Unwant)
	}
}

// Lidarr's bug: "Album + Live" there means any album OR anything live. Here both must
// pass: a live single isn't listed without Single, a live compilation not without
// Compilation, and a studio album still is.
func TestReleaseTypesUseAND(t *testing.T) {
	types := ReleaseTypes{Primary: []string{"Album"}, Secondary: []string{"Live"}}
	for _, c := range []struct {
		primary   string
		secondary []string
		want      bool
	}{
		{"Album", nil, true},
		{"Album", []string{"Live"}, true},
		{"Single", []string{"Live"}, false},
		{"Album", []string{"Compilation", "Live"}, false},
		{"EP", nil, false},
		{"", nil, true},              // no type: shown, never hidden
		{"", []string{"Live"}, true}, // its secondary types still count
		{"", []string{"Remix"}, false},
		{"album", []string{"live"}, true}, // case doesn't matter
	} {
		if got := types.Allows(c.primary, c.secondary); got != c.want {
			t.Errorf("Allows(%q, %v) = %v, want %v", c.primary, c.secondary, got, c.want)
		}
	}

	// Through a sync: the live single and the live compilation stay out.
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{
		rg("rg-studio", "OK Computer", "Album", "1997-05-21"),
		rg("rg-live", "I Might Be Wrong", "Album", "2001-11-12", "Live"),
		rg("rg-livecomp", "Live Hits", "Album", "2008-01-01", "Compilation", "Live"),
	}
	f.p.other["mb-rh"] = []metadata.AlbumResult{rg("rg-livesingle", "Creep (Live)", "Single", "1993-01-01", "Live")}
	if _, err := f.svc.AddArtistWith(f.ctx, "mb-rh", "", AddOptions{Monitored: true, ReleaseTypes: &types}); err != nil {
		t.Fatal(err)
	}
	for mbid, want := range map[string]bool{"rg-studio": true, "rg-live": true, "rg-livecomp": false, "rg-livesingle": false} {
		if f.hasAlbum(mbid) != want {
			t.Errorf("%s listed = %v, want %v", mbid, !want, want)
		}
	}
	if f.p.calls["other"] != 0 {
		t.Errorf("the singles browse was asked %d times without Single ticked", f.p.calls["other"])
	}
	// The default reproduces the old list exactly: albums and EPs, nothing secondary.
	def := DefaultReleaseTypes()
	if !def.Allows("Album", nil) || !def.Allows("EP", nil) || def.Allows("Album", []string{"Live"}) || def.Allows("Single", nil) || def.WantsOther() {
		t.Error("the default types changed what Music lists")
	}
	if _, err := ParseReleaseTypes(`{"primary":["Album","Bootleg"]}`); err == nil {
		t.Error("an unknown type was accepted")
	}
	if _, err := ParseReleaseTypes(`{"primary":[],"secondary":["Live"]}`); err == nil {
		t.Error("no primary type was accepted")
	}
	if rt, err := ParseReleaseTypes("Album,EP,Live"); err != nil || rt.String() != `{"primary":["Album","EP"],"secondary":["Live"]}` {
		t.Errorf("the comma form = %v %v", rt, err)
	}
}

// MusicBrainz merged an album's release group: the album follows the new id (old one
// kept as musicbrainz_old), and no duplicate is added. A merged artist is followed too.
// A release group MusicBrainz removed is flagged, kept, files and monitoring included.
func TestRefreshFollowsMergesAndFlagsRemovals(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-okc", "OK Computer", "Album", "1997-05-21"), rg("rg-bends", "The Bends", "Album", "1995-03-13")}
	a, err := f.svc.AddArtist(f.ctx, "mb-rh", "", true)
	if err != nil {
		t.Fatal(err)
	}
	okc := f.album(t, "rg-okc")
	bends := f.album(t, "rg-bends")

	// The artist and OK Computer were merged; The Bends was removed.
	f.p.artistID["mb-rh"] = "mb-rh-new"
	f.p.main["mb-rh-new"] = []metadata.AlbumResult{rg("rg-okc-new", "OK Computer", "Album", "1997-05-21")}
	f.p.merged["rg-okc"] = "rg-okc-new"
	f.p.removed["rg-bends"] = true
	f.p.notFound = realNotFound(t)

	res, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 {
		t.Errorf("added %d albums, want none (the merged one is the same album)", len(res.Added))
	}
	if got := f.album(t, "rg-okc-new"); got.ID != okc.ID {
		t.Errorf("the merged album is row %d, want the same row %d", got.ID, okc.ID)
	}
	ids, _ := f.repo.ExternalIDs(f.ctx, "album", okc.ID)
	if len(ids) != 1 || ids[0].Source != "musicbrainz_old" || ids[0].Value != "rg-okc" {
		t.Errorf("album external ids = %+v, want musicbrainz_old rg-okc", ids)
	}
	art, _ := f.repo.GetArtist(f.ctx, a.ID)
	if art.MBID != "mb-rh-new" {
		t.Errorf("artist MBID = %q, want the merged-into id", art.MBID)
	}
	aids, _ := f.repo.ExternalIDs(f.ctx, "artist", a.ID)
	if len(aids) != 1 || aids[0].Value != "mb-rh" {
		t.Errorf("artist external ids = %+v, want musicbrainz_old mb-rh", aids)
	}
	gone := f.album(t, "rg-bends")
	if gone.ID != bends.ID || gone.StaleSince == "" || !gone.Monitored {
		t.Errorf("removed album: stale=%q monitored=%v, want flagged and otherwise untouched", gone.StaleSince, gone.Monitored)
	}
	// New albums still take the artist's credit after the artist's id moved.
	f.p.main["mb-rh-new"] = append(f.p.main["mb-rh-new"], rg("rg-amsp", "A Moon Shaped Pool", "Album", "2026-10-01"))
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-amsp"); al.ArtistCredit != "Radiohead" {
		t.Errorf("a new album after the artist merge has credit %q", al.ArtistCredit)
	}
}

// MusicBrainz down, or answering with nothing: nothing is added, removed, flagged or
// unmonitored; the artist is marked stale with a history line.
func TestRefreshWhenMusicBrainzIsDownChangesNothing(t *testing.T) {
	for _, mode := range []string{"down", "empty"} {
		t.Run(mode, func(t *testing.T) {
			f := newRefreshFixture(t)
			f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-okc", "OK Computer", "Album", "1997-05-21"), rg("rg-kida", "Kid A", "Album", "2000-10-02")}
			a, err := f.svc.AddArtist(f.ctx, "mb-rh", "", true)
			if err != nil {
				t.Fatal(err)
			}
			_ = f.svc.SetAlbumMonitored(f.ctx, f.album(t, "rg-kida").ID, false)
			switch mode {
			case "down":
				f.p.down = true
			case "empty":
				f.p.main["mb-rh"] = nil
			}
			res, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Unanswered || len(res.Added)+len(res.Stale)+len(res.Merged) != 0 {
				t.Errorf("result %+v, want unanswered and no changes", res)
			}
			if n := f.albumCount(t, a.ID); n != 2 {
				t.Errorf("%d albums, want both kept", n)
			}
			if !f.album(t, "rg-okc").Monitored || f.album(t, "rg-kida").Monitored {
				t.Error("monitoring changed")
			}
			if f.album(t, "rg-okc").StaleSince != "" {
				t.Error("an album was flagged on no answer")
			}
			art, _ := f.repo.GetArtist(f.ctx, a.ID)
			if art.StaleSince == "" {
				t.Error("the artist isn't marked stale")
			}
			evs, _ := f.repo.Events(f.ctx, a.ID, 5)
			if len(evs) == 0 || evs[0].Detail != "MusicBrainz didn't answer; kept what we had" {
				t.Errorf("latest event = %+v", evs)
			}
		})
	}
}

// A single is wanted only when its songs aren't already on one of the artist's monitored
// albums: matched by recording, or by title without its version suffix (the radio edit).
func TestSinglesAlreadyOnAnAlbumAreNotWanted(t *testing.T) {
	f := newRefreshFixture(t)
	singles := ReleaseTypes{Primary: []string{"Album", "Single"}}
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-okc", "OK Computer", "Album", "1997-05-21")}
	f.p.tracks["rg-okc"] = []metadata.TrackResult{
		{Title: "Airbag", Disc: 1, Number: 1, RecordingMBID: "rec-airbag"},
		{Title: "Paranoid Android", Disc: 1, Number: 2, RecordingMBID: "rec-pa"},
	}
	a, err := f.svc.AddArtistWith(f.ctx, "mb-rh", "", AddOptions{Monitored: true, ReleaseTypes: &singles})
	if err != nil {
		t.Fatal(err)
	}
	f.p.other["mb-rh"] = []metadata.AlbumResult{
		rg("rg-s-pa", "Paranoid Android", "Single", "2026-10-01"),
		rg("rg-s-airbag", "Airbag", "Single", "2026-10-02"),
		rg("rg-s-new", "Lift", "Single", "2026-10-05"),
	}
	f.p.tracks["rg-s-pa"] = []metadata.TrackResult{{Title: "Paranoid Android", Disc: 1, Number: 1, RecordingMBID: "rec-pa"}}
	f.p.tracks["rg-s-airbag"] = []metadata.TrackResult{{Title: "Airbag (Radio Edit)", Disc: 1, Number: 1, RecordingMBID: "rec-airbag-edit"}}
	f.p.tracks["rg-s-new"] = []metadata.TrackResult{
		{Title: "Lift", Disc: 1, Number: 1, RecordingMBID: "rec-lift"},
		{Title: "Paranoid Android", Disc: 1, Number: 2, RecordingMBID: "rec-pa"},
	}
	// The first look at the singles browse: these are recent, so new.
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rg-s-pa", "rg-s-airbag", "rg-s-new"} {
		if al := f.album(t, id); al.Monitored || al.FoundAs != FoundSinglePending {
			t.Fatalf("%s before the check: monitored=%v found=%q, want pending", id, al.Monitored, al.FoundAs)
		}
	}
	settled, wanted, err := f.svc.CheckPendingSingles(f.ctx, 10, func() bool { return true })
	if err != nil || settled != 3 || wanted != 1 {
		t.Fatalf("settled %d, wanted %d (%v); want 3 and 1", settled, wanted, err)
	}
	for id, want := range map[string]bool{"rg-s-pa": false, "rg-s-airbag": false, "rg-s-new": true} {
		al := f.album(t, id)
		if al.Monitored != want {
			t.Errorf("%s monitored = %v, want %v (found %q)", id, al.Monitored, want, al.FoundAs)
		}
		if !want && al.FoundAs != FoundSingleOnAlbum {
			t.Errorf("%s found = %q, want single_on_album", id, al.FoundAs)
		}
	}
	// A budget that says stop leaves the rest pending.
	f.p.other["mb-rh"] = append(f.p.other["mb-rh"], rg("rg-s-more", "Knives Out", "Single", "2026-10-10"))
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if settled, _, _ := f.svc.CheckPendingSingles(f.ctx, 10, func() bool { return false }); settled != 0 || f.album(t, "rg-s-more").FoundAs != FoundSinglePending {
		t.Errorf("with no budget %d settled, want the single left pending", settled)
	}
}

// "Latest" wants the newest album that is certainly out: never an announced one (a
// future date, or "2026" while 2026 isn't over), and an Album over a newer EP.
func TestLatestNeverPicksAnUnreleasedAlbum(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{
		rg("rg-future", "Announced", "Album", "2026-12-01"),
		rg("rg-thisyear", "This Year", "Album", "2026"),
		rg("rg-thismonth", "This Month", "Album", "2026-10"),
		rg("rg-ep", "New EP", "EP", "2026-03-01"),
		rg("rg-last", "Last Album", "Album", "2025-05-01"),
		rg("rg-old", "Old Album", "Album", "2016-05-08"),
	}
	if _, err := f.svc.AddArtistWith(f.ctx, "mb-rh", "", AddOptions{Monitored: true, Monitor: MonitorLatest}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"rg-future": false, "rg-thisyear": false, "rg-thismonth": false, "rg-ep": false, "rg-last": true, "rg-old": false} {
		if got := f.album(t, id).Monitored; got != want {
			t.Errorf("%s monitored = %v, want %v", id, got, want)
		}
	}
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for date, want := range map[string]bool{"": false, "2026": false, "2025": true, "2026-10": false, "2026-09": true, "2026-10-20": true, "2026-10-21": false, "junk": false} {
		if got := releasedBy(date, now); got != want {
			t.Errorf("releasedBy(%q) = %v, want %v", date, got, want)
		}
	}
	// "future" and "none" want nothing now; future still wants what comes.
	g := newRefreshFixture(t)
	g.p.main["mb-x"] = []metadata.AlbumResult{rg("rg-x1", "X", "Album", "2020-01-01")}
	a, err := g.svc.AddArtistWith(g.ctx, "mb-x", "", AddOptions{Monitored: true, Monitor: MonitorFuture})
	if err != nil {
		t.Fatal(err)
	}
	if g.album(t, "rg-x1").Monitored || a.MonitorNew != MonitorNewAll {
		t.Errorf("future: existing monitored=%v, monitor_new=%q", g.album(t, "rg-x1").Monitored, a.MonitorNew)
	}
	g.p.main["mb-x"] = append(g.p.main["mb-x"], rg("rg-x2", "Y", "Album", "2026-10-10"))
	if _, err := g.svc.RefreshArtist(g.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if !g.album(t, "rg-x2").Monitored {
		t.Error("future: the next release isn't wanted")
	}
}

// An upgraded library's first refresh is a first look: only releases from the last 90
// days count as new; older ones and undated ones are listed.
func TestFirstLookOnlyWantsRecentReleases(t *testing.T) {
	f := newRefreshFixture(t)
	// An artist as the upgrade leaves it: in the library, never refreshed, nothing seen.
	a, err := f.repo.CreateArtist(f.ctx, Artist{MBID: "mb-rh", Name: "Radiohead", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.UpsertAlbum(f.ctx, Album{ArtistID: a.ID, MBID: "rg-okc", Title: "OK Computer", AlbumType: "Album", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	f.p.main["mb-rh"] = []metadata.AlbumResult{
		rg("rg-okc", "OK Computer", "Album", "1997-05-21"),
		rg("rg-recent", "Recent", "Album", "2026-09-01"),
		rg("rg-2yo", "Two Years Old", "Album", "2024-09-01"),
		rg("rg-undated", "Undated", "Album", ""),
	}
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"rg-recent": FoundNew, "rg-2yo": FoundBackCatalogue, "rg-undated": FoundBackCatalogue} {
		al := f.album(t, id)
		if al.FoundAs != want || al.Monitored != (want == FoundNew) {
			t.Errorf("%s: found=%q monitored=%v, want %q", id, al.FoundAs, al.Monitored, want)
		}
	}
	// After the first look an undated newcomer is a new release (announcements often
	// have no date yet).
	f.p.main["mb-rh"] = append(f.p.main["mb-rh"], rg("rg-announced", "Announced", "Album", ""))
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-announced"); al.FoundAs != FoundNew || !al.Monitored {
		t.Errorf("undated newcomer: found=%q monitored=%v", al.FoundAs, al.Monitored)
	}
	if rel, err := f.svc.NewReleases(f.ctx, 30); err != nil || len(rel) != 4 || rel[0].ArtistName != "Radiohead" {
		t.Errorf("new releases = %d (%v)", len(rel), err)
	}
}
