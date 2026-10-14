package main

// A live test against a real Jellyfin, run by scripts/jellyfin-it.sh
// (JELLYFIN_IT_PKGS=./cmd/arrmada/ scripts/jellyfin-it.sh -run IT_Ready): the rig's
// throwaway server, its synthetic library, and its second mount (/arr) playing Arrmada's
// own folders. Skipped everywhere else.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/plexscan"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// A request for a film Jellyfin doesn't have yet waits ("Adding to Jellyfin…"); once the
// folder is reported and Jellyfin has scanned it in, the ready check finds it through the
// Registry and tells the requester "ready to watch on Jellyfin", with the Jellyfin web
// app's page for it at the public address: {public}/web/#/details?id=<item>&serverId=<id>.
func TestIT_ReadyWhenJellyfinHasIt(t *testing.T) {
	if os.Getenv("ARRMADA_JELLYFIN_IT") != "1" {
		t.Skip("live Jellyfin test: run scripts/jellyfin-it.sh (ARRMADA_JELLYFIN_IT=1)")
	}
	url, key, media, arr := os.Getenv("ARRMADA_JELLYFIN_URL"), os.Getenv("ARRMADA_JELLYFIN_KEY"), os.Getenv("ARRMADA_JELLYFIN_MEDIA"), os.Getenv("ARRMADA_JELLYFIN_ARR")
	if url == "" || key == "" || media == "" || arr == "" {
		t.Fatal("ARRMADA_JELLYFIN_URL, _KEY, _MEDIA and _ARR must be set")
	}
	t.Setenv("ARRMADA_JELLYFIN_API_KEY", "")
	const public = "https://watch.example.com"
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	set := settings.NewService(db)
	roots := func(context.Context) map[string]string {
		return map[string]string{plexscan.KindMovie: arr + "/Movies", plexscan.KindShow: arr + "/TV"}
	}
	jf := jellyfinsvc.New(jellyfinsvc.Options{Settings: set, Keys: apikeys.NewStore(set), Version: "0.1-it", Log: log, Roots: roots})
	if err := jf.Save(ctx, url, public, key); err != nil {
		t.Fatal(err)
	}
	serverID := jf.Config(ctx).ServerID
	if serverID == "" {
		t.Fatal("no server id after saving the connection")
	}
	sc := jf.NewScanner(set, roots, log)
	if err := sc.Save(ctx, true, []plexscan.PathMap{{From: arr + "/Movies", To: media + "/Movies"}}); err != nil {
		t.Fatal(err)
	}
	go sc.Run(ctx)
	go jf.RunIndex(ctx)
	loc := mediaReadyLocator{mediaserver.NewRegistry(jf)}

	// The film: a fresh TMDB id, its folder in Arrmada's spelling, nothing reported yet.
	tmdb := 920_000_000 + int(time.Now().Unix()%1_000_000)*10
	title := "Ready Eight"
	dir := filepath.Join(arr, "Movies", fmt.Sprintf("%s (2016) {tmdb-%d}", title, tmdb))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	clip, err := os.ReadFile(filepath.Join(media, "Movies", "The Matrix (1999) {tmdb-603}", "The Matrix (1999).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, title+" (2016).mkv")
	if err := os.WriteFile(file, clip, 0o644); err != nil {
		t.Fatal(err)
	}
	nfo := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<movie><title>%s</title><year>2016</year><uniqueid type="tmdb" default="true">%d</uniqueid></movie>
`, title, tmdb)
	if err := os.WriteFile(filepath.Join(dir, "movie.nfo"), []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}

	kid, err := auth.NewService(db).CreateUser(ctx, "kid", "password123", auth.RoleRequester, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability, has_file, movie_file_path) VALUES (1, ?, ?, 2016, 1, 'announced', 1, ?)`, tmdb, title, file); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO requests (media_type, tmdb_id, title, status, requested_by, requested_by_name) VALUES ('movie', ?, ?, 'approved', ?, 'kid')`, tmdb, title, kid.ID); err != nil {
		t.Fatal(err)
	}
	mv := movies.NewService(db, nil, nil, t.TempDir(), "", nil, log)
	rq := requests.NewService(db, mv, nil, nil, nil, nil, nil, "", log)
	rq.SetPlexLocator(loc)

	// Wait for the index's first build, then: complete on disk, not in Jellyfin — it waits.
	for !jf.IndexReady(ctx) {
		if ctx.Err() != nil {
			t.Fatal("the index never built")
		}
		time.Sleep(time.Second)
	}
	if err := rq.NotifyMovieReady(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if inbox, _ := rq.Inbox(ctx, kid.ID); len(inbox) != 0 {
		t.Fatalf("told before Jellyfin had it: %+v", inbox)
	}
	list, _, err := rq.List(ctx, requests.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	rq.Track(ctx, list, nil, true)
	if len(list) != 1 || list[0].Tracking == nil || list[0].Tracking.Stage != requests.StageAdding ||
		len(list[0].Tracking.AddingTo) != 1 || list[0].Tracking.AddingTo[0] != mediaserver.Jellyfin {
		t.Fatalf("waiting request = %+v", list[0].Tracking)
	}

	// Arrmada reports the folder; the ready check (every two minutes in the app, after
	// every index rebuild too) runs until Jellyfin has it.
	start := time.Now()
	sc.Request(plexscan.KindMovie, dir)
	var told []requests.UserNotification
	for len(told) == 0 {
		if time.Since(start) > 10*time.Minute {
			t.Fatalf("not announced %s after the report (last scan %+v)", time.Since(start).Round(time.Second), sc.LastScan())
		}
		time.Sleep(2 * time.Second)
		if err := rq.CheckPlexWaiting(ctx); err != nil {
			t.Fatal(err)
		}
		told, _ = rq.Inbox(ctx, kid.ID)
	}
	t.Logf("announced %s after the report", time.Since(start).Round(time.Second))
	n := told[0]
	if want := fmt.Sprintf("“%s” is ready to watch on Jellyfin.", title); n.Body != want {
		t.Errorf("body = %q, want %q", n.Body, want)
	}
	link := regexp.MustCompile(`^` + regexp.QuoteMeta(public) + `/web/#/details\?id=[0-9a-f]{32}&serverId=` + regexp.QuoteMeta(serverID) + `$`)
	if !link.MatchString(n.JellyfinURL) || n.PlexURL != "" {
		t.Errorf("inbox links: jellyfin %q plex %q, want %s", n.JellyfinURL, n.PlexURL, link)
	}
	if strings.Contains(n.JellyfinURL, key) || strings.Contains(n.JellyfinURL, strings.TrimPrefix(url, "http://")) {
		t.Errorf("the link carries the key or the connection address: %q", n.JellyfinURL)
	}
	// The locator's own answer is the same link, and the item behind it is the new film.
	seen := loc.Find(ctx, "movie", requests.PlexIDs{TMDB: tmdb})
	if len(seen) != 1 || seen[0].Link.URL != n.JellyfinURL || seen[0].Link.Label != "Jellyfin" {
		t.Errorf("find = %+v", seen)
	}
	it, ok := jf.Locate(ctx, "movie", mediaserver.ExternalIDs{TMDB: tmdb})
	if !ok || !strings.Contains(n.JellyfinURL, "id="+it.ID+"&") {
		t.Errorf("located %+v (%v), link %q", it, ok, n.JellyfinURL)
	}
}
