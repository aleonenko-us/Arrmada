package embysvc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/emby"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plexscan"
)

// The fixtures are representative Emby-shaped JSON for package tests, not live captures.
const fixtureServerID = "5e7e75e7e75e7e75e7e75e7e75e7e75e"

type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (f *memSettings) Get(_ context.Context, key, def string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.m[key]; ok && v != "" {
		return v
	}
	return def
}
func (f *memSettings) Set(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = value
	return nil
}
func (f *memSettings) GetBool(ctx context.Context, key string, def bool) bool {
	v := f.Get(ctx, key, "")
	if v == "" {
		return def
	}
	b, _ := strconv.ParseBool(v)
	return b
}
func (f *memSettings) SetBool(ctx context.Context, key string, v bool) error {
	return f.Set(ctx, key, strconv.FormatBool(v))
}

// fakeEmby answers like the rig's server, from the captured fixtures, for one key.
type fakeEmby struct {
	*httptest.Server
	key     string
	mu      sync.Mutex
	updates [][]emby.MediaUpdate
	refresh []string
	tokens  []string // the Token each call carried ("" for none)
	down    bool
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "emby", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newFakeEmby(t *testing.T, key string) *fakeEmby {
	t.Helper()
	f := &fakeEmby{key: key}
	pub, info := fixture(t, "system_info_public.json"), fixture(t, "system_info.json")
	vfs, items, recent := fixture(t, "virtual_folders.json"), fixture(t, "items_index.json"), fixture(t, "recently_added.json")
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/emby/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/emby")
		}
		f.mu.Lock()
		down := f.down
		tok := r.Header.Get("X-Emby-Token")
		f.tokens = append(f.tokens, tok)
		f.mu.Unlock()
		if down {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/System/Info/Public" {
			_, _ = w.Write(pub)
			return
		}
		if tok != f.key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/System/Info":
			_, _ = w.Write(info)
		case r.URL.Path == "/Library/VirtualFolders":
			_, _ = w.Write(vfs)
		case r.URL.Path == "/Items" && r.URL.Query().Get("sortBy") == "DateCreated":
			_, _ = w.Write(recent)
		case r.URL.Path == "/Items":
			_, _ = w.Write(items)
		case r.URL.Path == "/Library/Media/Updated" && r.Method == http.MethodPost:
			var body struct{ Updates []emby.MediaUpdate }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.updates = append(f.updates, body.Updates)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/Items/") && strings.HasSuffix(r.URL.Path, "/Refresh"):
			f.mu.Lock()
			f.refresh = append(f.refresh, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/Items/"), "/Refresh"))
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeEmby) sentTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokens...)
}

type harness struct {
	s    *Service
	set  *memSettings
	keys *apikeys.Store
	jf   *fakeEmby
	maps []plexscan.PathMap
	root map[string]string
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("ARRMADA_JELLYFIN_API_KEY", "")
	h := &harness{set: &memSettings{m: map[string]string{}}, jf: newFakeEmby(t, "jf-key-123"),
		root: map[string]string{plexscan.KindMovie: "/movies", plexscan.KindShow: "/tv"}}
	h.keys = apikeys.NewStore(h.set)
	h.s = New(Options{
		Settings: h.set, Keys: h.keys, Version: "0.9-test", Log: quiet(),
		Roots:    func(context.Context) map[string]string { return h.root },
		PathMaps: func(context.Context) []plexscan.PathMap { return h.maps },
	})
	return h
}

func (h *harness) connect(t *testing.T) {
	t.Helper()
	if err := h.s.Save(context.Background(), h.jf.URL+"/", "", "jf-key-123"); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) build(t *testing.T) {
	t.Helper()
	if err := h.s.idx.rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSaveConfigAndIdentity(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if h.s.Configured(ctx) {
		t.Fatal("configured with nothing saved")
	}
	if err := h.s.Save(ctx, h.jf.URL, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("save without a key = %v", err)
	}
	for _, bad := range []string{"emby:8096", "ftp://x", "http://user:pw@jf:8096", ""} {
		if err := h.s.Save(ctx, bad, "", "k"); !errors.Is(err, ErrInvalid) {
			t.Errorf("save %q = %v", bad, err)
		}
	}
	// A public address the same as the URL follows the URL.
	if err := h.s.Save(ctx, h.jf.URL+"/", h.jf.URL, "jf-key-123"); err != nil {
		t.Fatal(err)
	}
	c := h.s.Config(ctx)
	want := Config{URL: h.jf.URL, PublicURL: "", KeySet: true, ServerName: "arrmada-it", ServerID: fixtureServerID, Version: "4.9.0.43", PollSeconds: DefaultPollSeconds}
	if c != want {
		t.Fatalf("config = %+v\nwant %+v", c, want)
	}
	if h.s.PublicURL(ctx) != h.jf.URL || h.keys.Value(ctx, apikeys.Emby) != "jf-key-123" || h.set.m[KeyURL] != h.jf.URL {
		t.Fatal("not saved where expected")
	}
	if b, _ := json.Marshal(c); strings.Contains(string(b), "jf-key-123") {
		t.Fatal("the config carries the key")
	}
	if h.s.Health(ctx).LastOKAt.IsZero() || h.s.Name(ctx) != "arrmada-it" {
		t.Fatalf("health %+v name %q", h.s.Health(ctx), h.s.Name(ctx))
	}
	// A device id is made once and kept.
	id := h.s.DeviceID(ctx)
	if !strings.HasPrefix(id, "arrmada-") || h.s.DeviceID(ctx) != id {
		t.Fatalf("device id %q", id)
	}
	// Keeping the key: a save without one leaves it; a separate public address stays.
	if err := h.s.Save(ctx, h.jf.URL, "https://jf.example.com/", ""); err != nil {
		t.Fatal(err)
	}
	if h.s.PublicURL(ctx) != "https://jf.example.com" || h.s.Config(ctx).PublicURL != "https://jf.example.com" || !h.s.Configured(ctx) {
		t.Fatalf("public %q", h.s.PublicURL(ctx))
	}
	// Another server: the old identity is forgotten (and this one doesn't answer).
	if err := h.s.Save(ctx, "http://127.0.0.1:1", "", ""); err != nil {
		t.Fatal(err)
	}
	if c := h.s.Config(ctx); c.ServerID != "" || c.ServerName != "" || c.Version != "" {
		t.Fatalf("identity kept across servers: %+v", c)
	}
	if hh := h.s.Health(ctx); hh.Consecutive == 0 || hh.LastErr == "" {
		t.Fatalf("an unreachable server isn't recorded: %+v", hh)
	}
}

// Test checks typed values without saving them, never echoes the key, and never sends the
// saved key anywhere but the saved address.
func TestTestNeverSendsSavedKeyElsewhere(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	other := newFakeEmby(t, "other-key")

	if r := h.s.Test(ctx, "", ""); r.OK || !strings.Contains(r.Detail, "address") {
		t.Fatalf("nothing saved = %+v", r)
	}
	if r := h.s.Test(ctx, "http://127.0.0.1:1", ""); r.OK || !strings.Contains(r.Detail, "reachable") {
		t.Fatalf("unreachable = %+v", r)
	}
	if r := h.s.Test(ctx, h.jf.URL, "wrong-key"); r.OK || !strings.Contains(r.Detail, "rejected the API key") || strings.Contains(r.Detail, "wrong-key") {
		t.Fatalf("wrong key = %+v", r)
	}
	if r := h.s.Test(ctx, h.jf.URL, "jf-key-123"); !r.OK || r.ServerName != "arrmada-it" || r.Version != "4.9.0.43" {
		t.Fatalf("typed values = %+v", r)
	}
	if h.s.Configured(ctx) || h.set.m[KeyURL] != "" {
		t.Fatal("Test saved something")
	}

	h.connect(t)
	if r := h.s.Test(ctx, "", ""); !r.OK || r.Detail != "Connected" {
		t.Fatalf("saved = %+v", r)
	}
	// Another address with no key typed: only its public answer, without the saved key.
	before := len(other.sentTokens())
	r := h.s.Test(ctx, other.URL, "")
	if r.OK || !strings.Contains(r.Detail, "Paste the API key") || r.ServerName != "arrmada-it" {
		t.Fatalf("other address, no key = %+v", r)
	}
	for _, tok := range other.sentTokens()[before:] {
		if tok != "" {
			t.Fatalf("a key went to another address: %q", tok)
		}
	}
	if r := h.s.Test(ctx, other.URL, "other-key"); !r.OK {
		t.Fatalf("other address with its key = %+v", r)
	}
}

func TestIndexFindsTitlesByIDsAndFolder(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if _, ok := h.s.Locate(ctx, "movie", mediaserver.ExternalIDs{TMDB: 603}); ok {
		t.Fatal("found before anything is set up")
	}
	h.connect(t)
	h.build(t)
	if !h.s.IndexReady(ctx) || h.s.IndexBuiltAt().IsZero() {
		t.Fatal("index not ready after a build")
	}
	ids := func(media string, x mediaserver.ExternalIDs) string {
		it, ok := h.s.Locate(ctx, media, x)
		if !ok {
			return ""
		}
		if it.Server != mediaserver.Emby || it.Media != media {
			t.Errorf("item %+v for %s", it, media)
		}
		return it.ID
	}
	for _, c := range []struct {
		media string
		x     mediaserver.ExternalIDs
		want  string
	}{
		{"movie", mediaserver.ExternalIDs{TMDB: 603}, "4071f1"},
		{"movie", mediaserver.ExternalIDs{IMDB: "tt1375666"}, "dbc971"},
		{"movie", mediaserver.ExternalIDs{TMDB: 348}, "873d94"},
		{"series", mediaserver.ExternalIDs{TVDB: 81189}, "220a6b"},
		{"series", mediaserver.ExternalIDs{TMDB: 1396}, "220a6b"},
		{"series", mediaserver.ExternalIDs{IMDB: "tt0903747"}, "220a6b"},
		{"movie", mediaserver.ExternalIDs{TMDB: 1396}, ""},   // a show's id isn't a movie
		{"movie", mediaserver.ExternalIDs{TMDB: 438631}, ""}, // Dune's files didn't group: no ids
	} {
		if got := ids(c.media, c.x); !strings.HasPrefix(got, c.want) || (c.want == "" && got != "") {
			t.Errorf("%s %+v = %q, want %s…", c.media, c.x, got, c.want)
		}
	}

	// The folder fallback: Arrmada's /srv/films is Emby's /media/Movies by a mapping…
	h.root = map[string]string{plexscan.KindMovie: "/srv/films", plexscan.KindShow: "/srv/shows"}
	dune := mediaserver.ExternalIDs{TMDB: 438631, Folder: "/srv/films/Dune (2021) {tmdb-438631}"}
	if got := ids("movie", dune); got != "" {
		t.Errorf("found Dune without a way to translate its folder: %s", got)
	}
	h.maps = []plexscan.PathMap{{From: "/srv/films", To: "/media/Movies"}}
	if got := ids("movie", dune); got == "" {
		t.Error("Dune not found by its mapped folder")
	}
	// …or the same folder name at the end (/mnt/user/movies ↔ /media/Movies), or the
	// same mount.
	h.maps = nil
	h.root = map[string]string{plexscan.KindMovie: "/mnt/user/movies", plexscan.KindShow: "/mnt/user/tv"}
	if got := ids("movie", mediaserver.ExternalIDs{Folder: "/mnt/user/movies/Heat (1995) {tmdb-949}"}); got == "" {
		t.Error("Heat not found by a guessed folder")
	}
	if got := ids("series", mediaserver.ExternalIDs{Folder: "/mnt/user/tv/Breaking Bad (2008) {tvdb-81189}"}); !strings.HasPrefix(got, "220a6b") {
		t.Errorf("show by folder = %q", got)
	}
	if got := ids("movie", mediaserver.ExternalIDs{Folder: "/media/Movies/Memento (2000)"}); !strings.HasPrefix(got, "99eb3a") {
		t.Errorf("same mount = %q", got)
	}
	if got := ids("movie", mediaserver.ExternalIDs{Folder: "/media/Movies"}); got != "" {
		t.Errorf("the library folder itself found %q", got)
	}

	// Watch links: the public address, the item and the server id; never the key.
	want := h.jf.URL + "/web/#/details?id=4071f1"
	u := h.s.WatchURL(ctx, "movie", mediaserver.ExternalIDs{TMDB: 603})
	if !strings.HasPrefix(u, want) || !strings.HasSuffix(u, "&serverId="+fixtureServerID) || strings.Contains(u, "jf-key") {
		t.Fatalf("watch url = %s", u)
	}
	if err := h.s.Save(ctx, h.jf.URL, "https://jf.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if u := h.s.WatchURL(ctx, "movie", mediaserver.ExternalIDs{TMDB: 603}); !strings.HasPrefix(u, "https://jf.example.com/web/#/details?id=4071f1") {
		t.Fatalf("watch url with a public address = %s", u)
	}
	if u := h.s.WatchURL(ctx, "movie", mediaserver.ExternalIDs{TMDB: 1}); u != "" {
		t.Fatalf("watch url for a title Emby lacks = %s", u)
	}
	// Through the Registry, next to no Plex.
	reg := mediaserver.NewRegistry(h.s)
	if links := reg.WatchLinks(ctx, "series", mediaserver.ExternalIDs{TVDB: 81189}); len(links) != 1 || links[0].Label != "Emby" {
		t.Fatalf("registry links = %+v", links)
	}
}

func TestRecentlyAddedTMDB(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if got, err := h.s.RecentlyAddedTMDB(ctx, 60); err != nil || got != nil {
		t.Fatalf("not set up = %v %v", got, err)
	}
	h.connect(t)
	h.build(t)
	got, err := h.s.RecentlyAddedTMDB(ctx, 60)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, r := range got {
		keys = append(keys, r.Media+":"+strconv.Itoa(r.TMDB))
		if r.AddedAt == 0 {
			t.Errorf("%+v has no time", r)
		}
	}
	// The episode counts as its show; titles without a TMDB id are left out.
	want := []string{"series:1396", "movie:348", "movie:578", "movie:577922", "movie:49047", "movie:27205", "movie:603"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("recent = %v\nwant %v", keys, want)
	}
}

// The scanner Emby gets: its own settings, its virtual folders as libraries, several
// folders in one report, and an index look ScanSettle after Emby took a report.
func TestScannerAdapter(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.connect(t)
	sc := h.s.NewScanner(h.set, func(context.Context) map[string]string {
		return map[string]string{plexscan.KindMovie: "/media/Movies", plexscan.KindShow: "/media/TV"}
	}, quiet())
	if !sc.Enabled(ctx) || !sc.Configured(ctx) {
		t.Fatal("scans on by default once Emby is connected")
	}
	if err := sc.Save(ctx, true, []plexscan.PathMap{{From: "/movies", To: "/media/Movies"}}); err != nil {
		t.Fatal(err)
	}
	if h.set.m[KeyPathMap] == "" || h.set.m[plexscan.KeyPathMap] != "" {
		t.Fatalf("path map saved under the wrong key: %v", h.set.m)
	}
	rv, err := sc.ScanNow(ctx, plexscan.KindMovie, true)
	if err != nil || rv.How != plexscan.HowDirect || rv.PlexPath != "/media/Movies" || !reflect.DeepEqual(rv.Sections, []string{"Movies"}) {
		t.Fatalf("scan now = %+v, %v", rv, err)
	}
	h.jf.mu.Lock()
	updates := h.jf.updates
	h.jf.mu.Unlock()
	if len(updates) != 1 || len(updates[0]) != 1 || updates[0][0].Path != "/media/Movies" || updates[0][0].UpdateType != emby.UpdateModified {
		t.Fatalf("updates = %+v", updates)
	}
	h.s.idx.mu.RLock()
	stale := h.s.idx.staleAt
	h.s.idx.mu.RUnlock()
	if d := time.Until(stale); d < ScanSettle-5*time.Second || d > ScanSettle {
		t.Fatalf("index look in %s, want about %s", d, ScanSettle)
	}

	// Several folders, one report; a whole library is its item's refresh.
	a := scanClient{h.s.Client(ctx)}
	if err := a.RefreshPaths(ctx, []string{"/media/Movies/A (2001)", "/media/TV/B (2002)"}); err != nil {
		t.Fatal(err)
	}
	if err := a.RefreshSection(ctx, "f137a2dd21bbc1b99aa5c0f6bf02a805"); err != nil {
		t.Fatal(err)
	}
	h.jf.mu.Lock()
	defer h.jf.mu.Unlock()
	if len(h.jf.updates) != 2 || len(h.jf.updates[1]) != 2 || !reflect.DeepEqual(h.jf.refresh, []string{"f137a2dd21bbc1b99aa5c0f6bf02a805"}) {
		t.Fatalf("updates %+v refresh %v", h.jf.updates, h.jf.refresh)
	}
}

func TestLibrariesKinds(t *testing.T) {
	got := libraries([]emby.VirtualFolder{
		{Name: "Films", CollectionType: "movies", ItemID: "1", Locations: []string{"/m"}},
		{Name: "Shows", CollectionType: "tvshows", ItemID: "2", Locations: []string{"/t"}},
		{Name: "Mixed", CollectionType: "", ItemID: "3", Locations: []string{"/x"}},
		{Name: "Music", CollectionType: "music", ItemID: "4", Locations: []string{"/a"}},
	})
	var kinds []string
	for _, l := range got {
		kinds = append(kinds, l.Key+":"+l.Type)
	}
	if want := []string{"1:movie", "2:show", "3:movie", "3:show"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("libraries = %v", kinds)
	}
}

func TestHealthRecordsRefusedKey(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.connect(t)
	if err := h.keys.Set(ctx, apikeys.Emby, "revoked"); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Probe(ctx); !errors.Is(err, emby.ErrUnauthorized) {
		t.Fatalf("probe = %v", err)
	}
	hh := h.s.Health(ctx)
	if !hh.Configured || hh.Monitoring || !hh.Unauthorized || hh.Consecutive != 1 || hh.FailingSince.IsZero() || strings.Contains(hh.LastErr, "revoked") {
		t.Fatalf("health = %+v", hh)
	}
	h.s.SetPolling(func(context.Context) bool { return true })
	if !h.s.Health(ctx).Monitoring {
		t.Fatal("a poller that records counts as monitoring")
	}
	h.s.Record(nil)
	if hh := h.s.Health(ctx); hh.Consecutive != 0 || hh.Unauthorized {
		t.Fatalf("after a good answer: %+v", hh)
	}
	// A server that answers 503 is a failure, not a refused key.
	h.jf.mu.Lock()
	h.jf.down = true
	h.jf.mu.Unlock()
	if err := h.keys.Set(ctx, apikeys.Emby, "jf-key-123"); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Probe(ctx); err == nil || h.s.Health(ctx).Unauthorized {
		t.Fatalf("503 = %v, %+v", err, h.s.Health(ctx))
	}
}

// Asks to look again push the rebuild back to the latest one, past the Plex index's
// five-minute cap: Emby doesn't scan until the reports stop.
func TestIndexStaleFollowsTheLastAsk(t *testing.T) {
	var l indexSchedule
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	l.idx = &index{builtAt: now}
	for i := 0; i < 10; i++ {
		l.markStale(ScanSettle)
		now = now.Add(time.Minute)
	}
	// The last ask was at 12:09; the rebuild is 70 s after it.
	if want := now.Add(-time.Minute).Add(ScanSettle); !l.staleAt.Equal(want) {
		t.Fatalf("stale at %s, want %s", l.staleAt.Format("15:04:05"), want.Format("15:04:05"))
	}
	if ok, wait := l.due(true); ok || wait != ScanSettle-time.Minute {
		t.Fatalf("due = %v in %s", ok, wait)
	}
	now = now.Add(ScanSettle)
	if ok, _ := l.due(true); !ok {
		t.Fatal("not due after the wait")
	}
}

// A rebuild that ran for another reason doesn't swallow a look still to come, and a scan
// gets a second look FollowUp after the first.
func TestIndexLooksAfterAScan(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	l := indexSchedule{now: func() time.Time { return now }}
	l.build = func(context.Context) (*index, error) { return &index{builtAt: now}, nil }
	if err := l.rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.markStale(ScanSettle)
	l.markFollowUp(ScanSettle + FollowUp)
	// Something else rebuilds now (a saved connection): both looks stay.
	if err := l.rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, wait := l.due(true); ok || wait != ScanSettle {
		t.Fatalf("after an early rebuild: due %v in %s, want the settle look", ok, wait)
	}
	now = now.Add(ScanSettle)
	if ok, _ := l.due(true); !ok {
		t.Fatal("settle look not due")
	}
	_ = l.rebuild(context.Background())
	if ok, wait := l.due(true); ok || wait != FollowUp {
		t.Fatalf("after the settle look: due %v in %s, want the follow-up", ok, wait)
	}
	now = now.Add(FollowUp)
	_ = l.rebuild(context.Background())
	if ok, wait := l.due(true); ok || wait != indexTTL {
		t.Fatalf("after the follow-up: due %v in %s, want the half-hourly look", ok, wait)
	}
}
