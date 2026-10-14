package jellyfin

// Live tests against a real Jellyfin. They skip unless ARRMADA_JELLYFIN_IT=1; run them with
// scripts/jellyfin-it.sh, which builds a throwaway server with synthetic media, a key, an
// admin, a test user and Quick Connect, and exports the variables read below. Every test
// name contains "IT" so the script can select them with -run IT.
//
// ARRMADA_JELLYFIN_CAPTURE=<dir> additionally writes every response (keys, tokens, secrets
// and the server id redacted) to <dir>, which is how testdata/ was captured.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type itEnv struct {
	url, key            string
	adminUser, adminPw  string
	user, userPw        string
	media               string
	hc                  *http.Client
	serverID            string
	movies, tv          VirtualFolder
	moviesLib, tvLib    string
	itemsByTMDB         map[int]Item
	itemsByTVDB         map[int]Item
	initOnce            sync.Once
	initErr             error
	adminCli, playerCli *Client
}

var (
	itOnce sync.Once
	itE    *itEnv
)

func liveEnv(t *testing.T) *itEnv {
	t.Helper()
	if os.Getenv("ARRMADA_JELLYFIN_IT") != "1" {
		t.Skip("live Jellyfin test: run scripts/jellyfin-it.sh (ARRMADA_JELLYFIN_IT=1)")
	}
	itOnce.Do(func() {
		e := &itEnv{
			url:       os.Getenv("ARRMADA_JELLYFIN_URL"),
			key:       os.Getenv("ARRMADA_JELLYFIN_KEY"),
			adminUser: os.Getenv("ARRMADA_JELLYFIN_ADMIN_USER"),
			adminPw:   os.Getenv("ARRMADA_JELLYFIN_ADMIN_PASSWORD"),
			user:      os.Getenv("ARRMADA_JELLYFIN_USER"),
			userPw:    os.Getenv("ARRMADA_JELLYFIN_PASSWORD"),
			media:     os.Getenv("ARRMADA_JELLYFIN_MEDIA"),
		}
		e.hc = defaultHTTPClient()
		if dir := os.Getenv("ARRMADA_JELLYFIN_CAPTURE"); dir != "" {
			e.hc.Transport = &captureTransport{dir: dir, next: e.hc.Transport, secrets: []string{e.key}}
		}
		itE = e
	})
	if itE.url == "" || itE.key == "" {
		t.Fatal("ARRMADA_JELLYFIN_URL and ARRMADA_JELLYFIN_KEY must be set")
	}
	return itE
}

func (e *itEnv) client() *Client { return New(e.url, e.key, "arrmada-it", "0.1-it", e.hc) }

// device is a client with no key, as a separate app on its own device id.
func (e *itEnv) device(id string) *Client { return New(e.url, "", id, "0.1-it", e.hc) }

func itCtx(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestIT_SystemInfo(t *testing.T) {
	e := liveEnv(t)
	ctx := itCtx(t, 30*time.Second)
	pub, err := e.device("arrmada-it-anon").PublicSystemInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	si, err := e.client().SystemInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if si.ID == "" || si.ID != pub.ID || si.Version == "" || si.ServerName == "" || !pub.StartupWizardCompleted {
		t.Fatalf("system info = %+v, public = %+v", si, pub)
	}
	t.Logf("server %q version %s id %s…", si.ServerName, si.Version, si.ID[:6])

	// SystemInfo needs the key; the public one doesn't.
	if _, err := e.device("arrmada-it-anon").SystemInfo(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("SystemInfo without a key = %v, want ErrNotConfigured before any request", err)
	}
	if _, err := New(e.url, "not-a-key", "arrmada-it", "0", e.hc).SystemInfo(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("wrong key = %v, want ErrUnauthorized", err)
	} else if strings.Contains(err.Error(), "not-a-key") {
		t.Errorf("error %q carries the key", err)
	}
}

// The auth variants a client could use. Only the MediaBrowser Authorization header (with or
// without the client fields) is accepted by 12.2; the test fails if that ever stops, and
// logs what the legacy forms do so a server upgrade that changes them is visible.
func TestIT_AuthHeaderVariants(t *testing.T) {
	e := liveEnv(t)
	ctx := itCtx(t, 30*time.Second)
	try := func(name string, mod func(*http.Request)) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+"/System/Info", nil)
		mod(req)
		resp, err := e.hc.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp.Body.Close()
		t.Logf("%-36s HTTP %d", name, resp.StatusCode)
		return resp.StatusCode
	}
	if s := try("Authorization: MediaBrowser (full)", func(r *http.Request) {
		r.Header.Set("Authorization", e.client().authorization())
	}); s != 200 {
		t.Errorf("full MediaBrowser header = %d", s)
	}
	if s := try("Authorization: MediaBrowser Token only", func(r *http.Request) {
		r.Header.Set("Authorization", `MediaBrowser Token="`+e.key+`"`)
	}); s != 200 {
		t.Errorf("token-only MediaBrowser header = %d", s)
	}
	try("X-Emby-Token", func(r *http.Request) { r.Header.Set("X-Emby-Token", e.key) })
	try("X-MediaBrowser-Token", func(r *http.Request) { r.Header.Set("X-MediaBrowser-Token", e.key) })
	try("X-Emby-Authorization", func(r *http.Request) {
		r.Header.Set("X-Emby-Authorization", `MediaBrowser Token="`+e.key+`"`)
	})
	try("?api_key=", func(r *http.Request) { r.URL.RawQuery = "api_key=" + e.key })
	try("?ApiKey=", func(r *http.Request) { r.URL.RawQuery = "ApiKey=" + e.key })
}

// setup fetches what several tests share: libraries and the provider-id index.
func (e *itEnv) setup(t *testing.T) {
	t.Helper()
	e.initOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		c := e.client()
		si, err := c.SystemInfo(ctx)
		if err != nil {
			e.initErr = err
			return
		}
		e.serverID = si.ID
		vfs, err := c.VirtualFolders(ctx)
		if err != nil {
			e.initErr = err
			return
		}
		for _, vf := range vfs {
			switch vf.CollectionType {
			case "movies":
				e.movies = vf
			case "tvshows":
				e.tv = vf
			}
		}
		items, err := c.AllItems(ctx, ItemsQuery{IncludeItemTypes: []string{TypeMovie, TypeSeries}, Recursive: true})
		if err != nil {
			e.initErr = err
			return
		}
		e.itemsByTMDB, e.itemsByTVDB = map[int]Item{}, map[int]Item{}
		for _, it := range items {
			if it.ProviderIDs.TMDB > 0 {
				e.itemsByTMDB[it.ProviderIDs.TMDB] = it
			}
			if it.ProviderIDs.TVDB > 0 {
				e.itemsByTVDB[it.ProviderIDs.TVDB] = it
			}
		}
	})
	if e.initErr != nil {
		t.Fatal(e.initErr)
	}
}

func TestIT_VirtualFolders(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	if e.movies.ItemID == "" || len(e.movies.Locations) != 1 || e.movies.Locations[0] != e.media+"/Movies" {
		t.Errorf("movies library = %+v", e.movies)
	}
	if e.tv.ItemID == "" || len(e.tv.Locations) != 1 || e.tv.Locations[0] != e.media+"/TV" {
		t.Errorf("tv library = %+v", e.tv)
	}
}

// The provider-id index finds every title, by NFO uniqueid or by a single-file folder's
// {tmdb-N} / [tmdbid-N] tag, with an API key and no userId.
func TestIT_ItemsIndex(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	for tmdb, title := range map[int]string{603: "The Matrix", 27205: "Inception", 348: "Alien", 578: "Jaws", 577922: "Tenet"} {
		it, ok := e.itemsByTMDB[tmdb]
		if !ok {
			t.Errorf("tmdb %d (%s) not in the index", tmdb, title)
			continue
		}
		if it.Type != TypeMovie || it.ID == "" || it.Path == "" || it.DateCreated.IsZero() {
			t.Errorf("tmdb %d = %+v", tmdb, it)
		}
	}
	if m := e.itemsByTMDB[603]; m.ProviderIDs.IMDB != "tt0133093" {
		t.Errorf("matrix imdb = %q", m.ProviderIDs.IMDB)
	}
	bb, ok := e.itemsByTVDB[81189]
	if !ok || bb.Type != TypeSeries || bb.ProviderIDs.TMDB != 1396 || bb.ProviderIDs.IMDB != "tt0903747" {
		t.Errorf("breaking bad = %+v (found %v)", bb, ok)
	}

	// With a userId the same titles come back: the key needs none, a user id only narrows.
	ctx := itCtx(t, 30*time.Second)
	users, err := e.client().Users(ctx)
	if err != nil || len(users) == 0 {
		t.Fatalf("users: %v %v", users, err)
	}
	all, err := e.client().Items(ctx, ItemsQuery{IncludeItemTypes: []string{TypeMovie, TypeSeries}, Recursive: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	var admin User
	for _, u := range users {
		if u.Name == e.adminUser {
			admin = u
		}
	}
	mine, err := e.client().Items(ctx, ItemsQuery{UserID: admin.ID, IncludeItemTypes: []string{TypeMovie, TypeSeries}, Recursive: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if all.TotalRecordCount == 0 || mine.TotalRecordCount != all.TotalRecordCount || len(all.Items) != 1 {
		t.Errorf("without userId %d items, with the admin's %d", all.TotalRecordCount, mine.TotalRecordCount)
	}

	// Paging: a page of 2 at a time sees every item exactly once.
	seen := map[string]bool{}
	for start := 0; start < all.TotalRecordCount; start += 2 {
		p, err := e.client().Items(ctx, ItemsQuery{IncludeItemTypes: []string{TypeMovie, TypeSeries}, Recursive: true, StartIndex: start, Limit: 2, SortBy: []string{"SortName"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			if seen[it.ID] {
				t.Errorf("item %s on two pages", it.ID)
			}
			seen[it.ID] = true
		}
	}
	if len(seen) != all.TotalRecordCount {
		t.Errorf("paged %d of %d", len(seen), all.TotalRecordCount)
	}
}

// How Jellyfin reads Arrmada's names. The rig has one folder per case; the test pins what
// 12.2 does so a change shows up, and logs the full picture.
func TestIT_NamingGrouping(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	ctx := itCtx(t, 30*time.Second)
	items, err := e.client().AllItems(ctx, ItemsQuery{
		ParentID: e.movies.ItemID, IncludeItemTypes: []string{TypeMovie}, Recursive: true,
		Fields: []string{"ProviderIds", "Path", "MediaSources"},
	})
	if err != nil {
		t.Fatal(err)
	}
	byFolder := map[string][]Item{}
	for _, it := range items {
		dir := filepath.Base(filepath.Dir(it.Path))
		byFolder[dir] = append(byFolder[dir], it)
		var versions []string
		for _, ms := range it.MediaSources {
			versions = append(versions, ms.Name)
		}
		t.Logf("%-40s %-34q tmdb=%-6d versions=%q", dir, it.Name, it.ProviderIDs.TMDB, versions)
	}
	expect := func(folder string, items, versions, tmdb int) {
		t.Helper()
		got := byFolder[folder]
		if len(got) != items {
			t.Errorf("%s: %d items, want %d", folder, len(got), items)
			return
		}
		if len(got[0].MediaSources) != versions {
			t.Errorf("%s: %d versions, want %d", folder, len(got[0].MediaSources), versions)
		}
		for _, it := range got {
			if it.ProviderIDs.TMDB != tmdb {
				t.Errorf("%s: %q tmdb=%d, want %d", folder, it.Name, it.ProviderIDs.TMDB, tmdb)
			}
		}
	}
	// Arrmada's default names in a {tmdb-N} folder: the files don't start with the folder's
	// name, so every file is its own movie and the folder's movie.nfo and id are ignored.
	expect("Dune (2021) {tmdb-438631}", 3, 1, 0)
	expect("Heat (1995) {tmdb-949}", 2, 1, 0)
	// A plain folder whose files are "<folder> - <label>" is one movie with versions…
	expect("Memento (2000)", 1, 2, 0)
	// …unless one file puts {edition-…} right after the year: then none are grouped.
	expect("Arrival (2016)", 3, 1, 0)
	// An edition after the label keeps the group (the label carries the edition).
	expect("Zodiac (2007)", 1, 2, 0)
	// File names that repeat the whole folder name, id tag included, group, and the folder
	// tag (or movie.nfo) gives the id.
	expect("Gravity (2013) {tmdb-49047}", 1, 2, 49047)
	expect("Tenet (2020) {tmdb-577922}", 1, 2, 577922)
	// Single-file folders always match: the folder tag in either style gives the id.
	expect("Jaws (1975) {tmdb-578}", 1, 1, 578)
	expect("Alien (1979) [tmdbid-348]", 1, 1, 348)
	// Hidden folders: a dot folder and a folder with a .ignore marker are skipped.
	if len(byFolder["Recycled (2004)"]) != 0 || len(byFolder["Ignored (2003)"]) != 0 {
		t.Errorf("hidden folders were scanned: %v %v", byFolder["Recycled (2004)"], byFolder["Ignored (2003)"])
	}
}

func TestIT_RecentlyAdded(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	ctx := itCtx(t, 30*time.Second)
	items, err := e.client().RecentlyAdded(ctx, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	var episode *Item
	for i, it := range items {
		if i > 0 && it.DateCreated.After(items[i-1].DateCreated) {
			t.Errorf("not newest first at %d", i)
		}
		if it.Type == TypeEpisode {
			episode = &items[i]
		}
		if it.Type != TypeMovie && it.Type != TypeEpisode {
			t.Errorf("unexpected type %q", it.Type)
		}
	}
	if episode == nil || episode.SeriesID != e.itemsByTVDB[81189].ID || episode.ProviderIDs.TVDB != 349232 {
		t.Errorf("episode = %+v", episode)
	}
	if few, err := e.client().RecentlyAdded(ctx, 2, ""); err != nil || len(few) != 2 {
		t.Errorf("limit 2 = %d items, %v", len(few), err)
	}
}

// A folder Arrmada just imported appears after LibraryMediaUpdated — with realtime
// monitoring off, so the call is what finds it — and the test logs how long it took.
func TestIT_LibraryMediaUpdated(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	if e.media == "" {
		t.Skip("ARRMADA_JELLYFIN_MEDIA not set: the rig's media isn't mounted here")
	}
	ctx := itCtx(t, 10*time.Minute)
	c := e.client()

	// One report: the item shows up after the server's library monitor delay.
	a, aDir := addMovie(t, e, "Arrival Two", 0)
	if err := c.LibraryMediaUpdated(ctx, []MediaUpdate{{Path: aDir, UpdateType: UpdateCreated}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	t.Logf("one report → item visible after %s", waitTMDB(ctx, t, e, a, start).Round(time.Second))

	// Two reports 30 s apart under the same library folder: is the delay a debounce (the
	// second report pushes the first one back) or a fixed timer per report?
	b, bDir := addMovie(t, e, "Arrival Three", 1)
	if err := c.LibraryMediaUpdated(ctx, []MediaUpdate{{Path: bDir, UpdateType: UpdateCreated}}); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	time.Sleep(30 * time.Second)
	cc, cDir := addMovie(t, e, "Arrival Four", 2)
	if err := c.LibraryMediaUpdated(ctx, []MediaUpdate{{Path: cDir, UpdateType: UpdateCreated}}); err != nil {
		t.Fatal(err)
	}
	first := waitTMDB(ctx, t, e, b, start)
	second := waitTMDB(ctx, t, e, cc, start)
	t.Logf("two reports 30 s apart → first visible after %s, second after %s (from the first report)",
		first.Round(time.Second), second.Round(time.Second))
}

// addMovie writes a new single-file movie folder with an NFO under the rig's Movies library
// and returns its (made-up, unique per run) TMDB id and the folder.
func addMovie(t *testing.T, e *itEnv, title string, n int) (int, string) {
	t.Helper()
	tmdb := 900_000_000 + int(time.Now().Unix()%10_000_000)*10 + n
	name := fmt.Sprintf("%s (2016) {tmdb-%d}", title, tmdb)
	dir := filepath.Join(e.media, "Movies", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(e.media, "Movies", "The Matrix (1999) {tmdb-603}", "The Matrix (1999).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, title+" (2016).mkv"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	nfo := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<movie><title>%s</title><year>2016</year><uniqueid type="tmdb" default="true">%d</uniqueid></movie>
`, title, tmdb)
	if err := os.WriteFile(filepath.Join(dir, "movie.nfo"), []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmdb, dir
}

// waitTMDB polls the Movies library until an item with the TMDB id appears and returns the
// time since start.
func waitTMDB(ctx context.Context, t *testing.T, e *itEnv, tmdb int, start time.Time) time.Duration {
	t.Helper()
	c := e.client()
	for {
		items, err := c.AllItems(ctx, ItemsQuery{ParentID: e.movies.ItemID, IncludeItemTypes: []string{TypeMovie}, Recursive: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.ProviderIDs.TMDB == tmdb {
				return time.Since(start)
			}
		}
		if time.Since(start) > 4*time.Minute {
			t.Fatalf("tmdb %d didn't appear within %s", tmdb, time.Since(start).Round(time.Second))
		}
		time.Sleep(2 * time.Second)
	}
}

func TestIT_RefreshLibraryAndItem(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	ctx := itCtx(t, 30*time.Second)
	c := e.client()
	if err := c.RefreshItem(ctx, e.itemsByTMDB[603].ID, false); err != nil {
		t.Errorf("refresh item: %v", err)
	}
	if err := c.RefreshItem(ctx, e.tv.ItemID, false); err != nil {
		t.Errorf("refresh library item: %v", err)
	}
	if err := c.RefreshItem(ctx, "00000000000000000000000000000001", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("refresh of a missing item = %v, want ErrNotFound", err)
	}
	if err := c.RefreshLibrary(ctx); err != nil {
		t.Errorf("refresh library: %v", err)
	}
}

func TestIT_ItemImageAndWatchURL(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	ctx := itCtx(t, 30*time.Second)
	// The web app answers at /web/ and registers the "details" hash route.
	resp, err := e.hc.Get(e.url + "/web/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(strings.ToLower(string(body)), "jellyfin") {
		t.Errorf("/web/ = HTTP %d", resp.StatusCode)
	}
	u := WatchURL(e.url+"/", e.serverID, e.itemsByTMDB[603].ID)
	if u != e.url+"/web/#/details?id="+e.itemsByTMDB[603].ID+"&serverId="+e.serverID {
		t.Errorf("watch url = %s", u)
	}
	// The synthetic library has no artwork (fetchers are off), so an image is a clean 404.
	if _, err := e.client().Image(ctx, e.itemsByTMDB[603].ID, ImagePrimary, 300); !errors.Is(err, ErrNotFound) {
		t.Errorf("image = %v, want ErrNotFound for an item without artwork", err)
	}
}

func TestIT_AuthenticateAndLogout(t *testing.T) {
	e := liveEnv(t)
	ctx := itCtx(t, 30*time.Second)
	dev := e.device("arrmada-it-signin")
	if _, err := dev.AuthenticateByName(ctx, e.user, e.userPw+"x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password = %v, want ErrUnauthorized", err)
	} else if strings.Contains(err.Error(), e.userPw) {
		t.Fatalf("error %q carries the password", err)
	}
	r, err := dev.AuthenticateByName(ctx, e.user, e.userPw)
	if err != nil {
		t.Fatal(err)
	}
	if r.User.Name != e.user || r.User.ID == "" || r.AccessToken == "" || r.User.Policy.IsAdministrator {
		t.Fatalf("auth = %+v", r.User)
	}
	as := dev.WithToken(r.AccessToken)
	var me User
	if err := as.call(ctx, http.MethodGet, "/Users/Me", nil, nil, &me, true); err != nil || me.ID != r.User.ID {
		t.Fatalf("token doesn't work: %v %+v", err, me)
	}
	// A plain user's token can't do admin things. (12.2 does let it list /Users.)
	if _, err := as.VirtualFolders(ctx); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-admin VirtualFolders = %v, want ErrForbidden", err)
	}
	if us, err := as.Users(ctx); err == nil {
		t.Logf("a non-admin token lists /Users: %d accounts", len(us))
	}
	if err := as.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if err := as.call(ctx, http.MethodGet, "/Users/Me", nil, nil, &me, true); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("token after logout = %v, want ErrUnauthorized", err)
	}

	// The admin flag reads through. The device id has a comma and a quote, which the header
	// escapes: Jellyfin must file the session under the id as given.
	odd := `arrmada-it,"odd" device`
	a, err := e.device(odd).AuthenticateByName(ctx, e.adminUser, e.adminPw)
	if err != nil {
		t.Fatal(err)
	}
	if !a.User.Policy.IsAdministrator {
		t.Errorf("admin = %+v", a.User.Policy)
	}
	ss, err := e.client().Sessions(ctx, 60)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range ss {
		found = found || s.DeviceID == odd
	}
	if !found {
		t.Errorf("no session filed under device %q", odd)
	}
	_ = e.device(odd).WithToken(a.AccessToken).Logout(ctx)
}

func TestIT_QuickConnect(t *testing.T) {
	e := liveEnv(t)
	ctx := itCtx(t, time.Minute)
	app := e.device("arrmada-it-quickconnect")
	on, err := app.QuickConnectEnabled(ctx)
	if err != nil || !on {
		t.Fatalf("quick connect enabled = %v, %v", on, err)
	}
	st, err := app.QuickConnectInitiate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Secret == "" || st.Code == "" || st.Authenticated {
		t.Fatalf("initiate = %+v", st)
	}
	if st2, err := app.QuickConnectConnect(ctx, st.Secret); err != nil || st2.Authenticated || st2.Code != st.Code {
		t.Fatalf("before approval = %+v, %v", st2, err)
	}
	if _, err := app.AuthenticateWithQuickConnect(ctx, st.Secret); err == nil {
		t.Fatal("an unapproved secret signed in")
	} else {
		t.Logf("unapproved secret: %v", err)
	}
	if _, err := app.QuickConnectConnect(ctx, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("unknown secret accepted")
	} else {
		t.Logf("unknown secret: %v", err)
	}

	// Someone signed in to Jellyfin as the admin types the code in.
	adminDev := e.device("arrmada-it-qc-approver")
	a, err := adminDev.AuthenticateByName(ctx, e.adminUser, e.adminPw)
	if err != nil {
		t.Fatal(err)
	}
	approver := adminDev.WithToken(a.AccessToken)
	defer func() { _ = approver.Logout(context.Background()) }()
	if err := approver.QuickConnectAuthorize(ctx, st.Code); err != nil {
		t.Fatal(err)
	}
	if st2, err := app.QuickConnectConnect(ctx, st.Secret); err != nil || !st2.Authenticated {
		t.Fatalf("after approval = %+v, %v", st2, err)
	}
	r, err := app.AuthenticateWithQuickConnect(ctx, st.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if r.User.Name != e.adminUser || r.AccessToken == "" {
		t.Fatalf("quick connect sign-in = %+v", r.User)
	}
	if err := app.WithToken(r.AccessToken).Logout(ctx); err != nil {
		t.Error(err)
	}
	// 12.2 does NOT retire an approved secret after its first sign-in: it signs in again
	// until the request expires. Arrmada has to forget the secret itself once it's used.
	if again, err := app.AuthenticateWithQuickConnect(ctx, st.Secret); err == nil {
		t.Logf("an approved secret signs in again after first use")
		_ = app.WithToken(again.AccessToken).Logout(ctx)
	} else {
		t.Logf("an approved secret is single use: %v", err)
	}
}

// playback reports as a player would.
func playback(ctx context.Context, c *Client, event string, body map[string]any) error {
	path := "/Sessions/Playing"
	if event != "" {
		path += "/" + event
	}
	return c.call(ctx, http.MethodPost, path, nil, body, nil, true)
}

func findSession(ctx context.Context, t *testing.T, e *itEnv, userName, itemID string) *Session {
	t.Helper()
	ss, err := e.client().Sessions(ctx, 960)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ss {
		if ss[i].UserName == userName && ss[i].NowPlayingItem != nil && ss[i].NowPlayingItem.ID == itemID {
			return &ss[i]
		}
	}
	return nil
}

// A direct-play session reported by a player shows in /Sessions with everything Insights
// reads; a real transcode (a stream request) adds TranscodingInfo.
func TestIT_Sessions(t *testing.T) {
	e := liveEnv(t)
	e.setup(t)
	ctx := itCtx(t, 2*time.Minute)
	dev := e.device("arrmada-it-player")
	r, err := dev.AuthenticateByName(ctx, e.user, e.userPw)
	if err != nil {
		t.Fatal(err)
	}
	player := dev.WithToken(r.AccessToken)
	defer func() { _ = player.Logout(context.Background()) }()

	matrix := e.itemsByTMDB[603]
	base := map[string]any{"ItemId": matrix.ID, "MediaSourceId": matrix.ID, "PlayMethod": "DirectPlay", "PlaySessionId": "it-direct", "CanSeek": true}
	with := func(kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	if err := playback(ctx, player, "", with("PositionTicks", 0)); err != nil {
		t.Fatal(err)
	}
	if err := playback(ctx, player, "Progress", with("PositionTicks", DurationToTicks(4*time.Second), "IsPaused", true, "EventName", "pause")); err != nil {
		t.Fatal(err)
	}
	s := findSession(ctx, t, e, e.user, matrix.ID)
	if s == nil {
		t.Fatal("the playing session isn't in /Sessions")
	}
	t.Logf("direct play: client=%q device=%q remote=%q method=%q paused=%v pos=%s transcoding=%v",
		s.Client, s.DeviceName, s.RemoteEndPoint, s.PlayState.PlayMethod, s.PlayState.IsPaused, s.PlayState.Position(), s.TranscodingInfo != nil)
	if s.UserID != r.User.ID || s.Client != ClientName || s.DeviceID != "arrmada-it-player" || s.RemoteEndPoint == "" {
		t.Errorf("session identity = %+v", s)
	}
	if s.PlayState.PlayMethod != "DirectPlay" || !s.PlayState.IsPaused || s.PlayState.Position() != 4*time.Second || s.PlayState.MediaSourceID != matrix.ID {
		t.Errorf("play state = %+v", s.PlayState)
	}
	np := s.NowPlayingItem
	if np.Type != TypeMovie || np.Name != "The Matrix" || np.ProviderIDs.TMDB != 603 || np.RunTimeTicks <= 0 {
		t.Errorf("now playing = %+v", np)
	}
	v := np.VideoStream()
	if v == nil || v.Codec != "h264" || v.BitDepth != 8 || v.Width != 320 {
		t.Errorf("now playing video stream = %+v", v)
	} else {
		t.Logf("now playing video: codec=%s profile=%q bitdepth=%d bitrate=%d range=%s", v.Codec, v.Profile, v.BitDepth, v.BitRate, v.VideoRange)
	}
	if s.TranscodingInfo != nil {
		t.Logf("direct play carries TranscodingInfo: %+v", s.TranscodingInfo)
	}
	if err := playback(ctx, player, "Stopped", with("PositionTicks", DurationToTicks(6*time.Second))); err != nil {
		t.Fatal(err)
	}
	if s := findSession(ctx, t, e, e.user, matrix.ID); s != nil {
		t.Errorf("still playing after Stopped: %+v", s.NowPlayingItem)
	}

	t.Run("transcode", func(t *testing.T) { transcodeSession(ctx, t, e, dev, player) })
}

// transcodeSession asks for a real transcode (HEVC 10-bit → H.264) and reads the session
// while ffmpeg runs.
func transcodeSession(ctx context.Context, t *testing.T, e *itEnv, dev, player *Client) {
	inc := e.itemsByTMDB[27205]
	ps := fmt.Sprintf("it-transcode-%d", time.Now().UnixNano())
	if err := playback(ctx, player, "", map[string]any{"ItemId": inc.ID, "MediaSourceId": inc.ID, "PlayMethod": "Transcode", "PlaySessionId": ps}); err != nil {
		t.Fatal(err)
	}
	q := fmt.Sprintf("/Videos/%s/stream.ts?static=false&mediaSourceId=%s&playSessionId=%s&deviceId=%s&videoCodec=h264&audioCodec=aac&maxWidth=320&videoBitRate=300000&audioBitRate=64000",
		inc.ID, inc.ID, ps, dev.deviceID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+q, nil)
	req.Header.Set("Authorization", player.authorization())
	resp, err := e.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream = HTTP %d", resp.StatusCode)
	}
	var read atomic.Int64
	go func() { // drain slowly so the job stays alive while we look
		buf := make([]byte, 8<<10)
		for {
			n, err := resp.Body.Read(buf)
			read.Add(int64(n))
			if err != nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		s := findSession(ctx, t, e, e.user, inc.ID)
		if s != nil && s.TranscodingInfo != nil {
			ti := s.TranscodingInfo
			t.Logf("transcode: method=%q video=%s audio=%s container=%s bitrate=%d hw=%q direct(v/a)=%v/%v %dx%d reasons=%v",
				s.PlayState.PlayMethod, ti.VideoCodec, ti.AudioCodec, ti.Container, ti.Bitrate, ti.HardwareAccelerationType,
				ti.IsVideoDirect, ti.IsAudioDirect, ti.Width, ti.Height, ti.TranscodeReasons)
			if ti.VideoCodec != "h264" || ti.Bitrate <= 0 || ti.IsVideoDirect {
				t.Errorf("transcoding info = %+v", ti)
			}
			if v := s.NowPlayingItem.VideoStream(); v == nil || v.BitDepth != 10 || v.Codec != "hevc" {
				t.Errorf("source stream = %+v", v)
			} else {
				t.Logf("source video: codec=%s profile=%q bitdepth=%d", v.Codec, v.Profile, v.BitDepth)
			}
			_ = playback(ctx, player, "Stopped", map[string]any{"ItemId": inc.ID, "MediaSourceId": inc.ID, "PlaySessionId": ps, "PositionTicks": 0})
			return
		}
		// The info only lives while ffmpeg runs, and a small clip transcodes fast.
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("no TranscodingInfo appeared (read %d bytes of the stream)", read.Load())
}

// captureTransport writes each response to dir for fixtures, with credentials redacted.
type captureTransport struct {
	dir     string
	next    http.RoundTripper
	secrets []string
	n       atomic.Int64
}

var (
	reTokenField = regexp.MustCompile(`"(AccessToken|Secret|DeviceId)":"[^"]*"`)
	reServerID   = regexp.MustCompile(`"ServerId":"[0-9a-f]{32}"`)
)

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(r)
	if err != nil || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return resp, err
	}
	body, rerr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	if rerr != nil {
		return resp, nil
	}
	s := reTokenField.ReplaceAllString(string(body), `"$1":"REDACTED"`)
	s = reServerID.ReplaceAllString(s, `"ServerId":"5e7e75e7e75e7e75e7e75e7e75e7e75e"`)
	for _, sec := range c.secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "REDACTED")
		}
	}
	var pretty any
	if json.Unmarshal([]byte(s), &pretty) == nil {
		if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			s = string(b)
		}
	}
	name := fmt.Sprintf("%03d_%s%s.json", c.n.Add(1), r.Method, strings.ReplaceAll(r.URL.Path, "/", "_"))
	_ = os.MkdirAll(c.dir, 0o755)
	_ = os.WriteFile(filepath.Join(c.dir, name), []byte(s+"\n"), 0o644)
	return resp, nil
}
