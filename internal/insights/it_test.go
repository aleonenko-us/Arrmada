package insights

// Live test against a real Jellyfin: run with scripts/jellyfin-it.sh (ARRMADA_JELLYFIN_IT=1),
// which builds a throwaway server with synthetic media, an API key and a test user. A player
// reports a play through /Sessions/Playing with the test user's own token, the way a
// Jellyfin app does, and Insights' poller has to record exactly one play of it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/jellyfin"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

func TestIT_InsightsRecordsJellyfinPlay(t *testing.T) {
	if os.Getenv("ARRMADA_JELLYFIN_IT") != "1" {
		t.Skip("live Jellyfin test: run scripts/jellyfin-it.sh (ARRMADA_JELLYFIN_IT=1)")
	}
	base, key := os.Getenv("ARRMADA_JELLYFIN_URL"), os.Getenv("ARRMADA_JELLYFIN_KEY")
	user, pw := os.Getenv("ARRMADA_JELLYFIN_USER"), os.Getenv("ARRMADA_JELLYFIN_PASSWORD")
	if base == "" || key == "" || user == "" {
		t.Fatal("ARRMADA_JELLYFIN_URL, _KEY and _USER must be set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	set := settings.NewService(st.DB())
	jf := jellyfinsvc.New(jellyfinsvc.Options{Settings: set, Keys: fakeKeys{apikeys.Jellyfin: key}, Version: "0.1-it", Log: log})
	if err := set.Set(ctx, jellyfinsvc.KeyURL, base); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st.DB(), set, nil, nil, log)
	svc.SetJellyfin(jf)
	on := true
	if err := svc.SetJellyfinMonitoring(ctx, &on, nil); err != nil {
		t.Fatal(err)
	}
	w := svc.jfw.Load()
	jf.SetPolling(svc.jellyfinMonitoring)
	m := monitor{src: w.src, tr: svc.jf, active: svc.jellyfinMonitoring, every: jf.PollSeconds, record: jf.Record, after: w.src.syncUsers}

	// The movie to play: The Matrix, a 10-second synthetic clip.
	page, err := jf.Client(ctx).Items(ctx, jellyfin.ItemsQuery{IncludeItemTypes: []string{jellyfin.TypeMovie}, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	var matrix jellyfin.Item
	for _, it := range page.Items {
		if it.Name == "The Matrix" {
			matrix = it
		}
	}
	if matrix.ID == "" {
		t.Fatal("The Matrix isn't in the library")
	}

	// A player app signs in as the test user and reports its playback with its own token.
	app := jellyfin.New(base, "", "arrmada-it-insights-player", "0.1-it", nil)
	auth, err := app.AuthenticateByName(ctx, user, pw)
	if err != nil {
		t.Fatal(err)
	}
	player := app.WithToken(auth.AccessToken)
	defer func() { _ = player.Logout(context.Background()) }()
	report := func(event string, pos time.Duration, paused bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"ItemId": matrix.ID, "MediaSourceId": matrix.ID, "PlayMethod": "DirectPlay", "PlaySessionId": "it-insights",
			"CanSeek": true, "PositionTicks": jellyfin.DurationToTicks(pos), "IsPaused": paused,
		})
		path := "/Sessions/Playing"
		if event != "" {
			path += "/" + event
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s", Client="Arrmada IT Player", Device="IT TV", DeviceId="arrmada-it-insights-player", Version="0.1-it"`, auth.AccessToken))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			t.Fatalf("%s = HTTP %d", path, resp.StatusCode)
		}
	}

	report("", 0, false)
	started := time.Now()
	svc.poll(ctx, m)
	if len(svc.jf.live) != 1 {
		t.Fatalf("after the start, following %d streams", len(svc.jf.live))
	}
	if !svc.Watching() {
		t.Error("a playing Jellyfin stream isn't 'watching'")
	}
	time.Sleep(3 * time.Second)
	report("Progress", 3*time.Second, false)
	svc.poll(ctx, m)
	time.Sleep(3 * time.Second)
	report("Progress", 6*time.Second, false)
	svc.poll(ctx, m)
	lastSeen := time.Now()
	report("Stopped", 6*time.Second, false)
	svc.poll(ctx, m) // gone: the play is recorded

	if h := jf.Health(ctx); h.LastOKAt.IsZero() || h.Consecutive != 0 || !h.Monitoring {
		t.Errorf("jellyfin health after the polls = %+v", h)
	}
	rows, total, err := svc.repo.history(ctx, HistoryFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("recorded %d plays, want exactly 1: %+v", total, rows)
	}
	r := rows[0]
	t.Logf("recorded: server=%s title=%q user=%q span=%ds offset=%dms duration=%dms decision=%s player=%q platform=%q ip=%s location=%s video=%q",
		r.Server, r.Title, r.UserName, r.StoppedAt-r.StartedAt, r.ViewOffsetMS, r.DurationMS, r.Decision, r.Player, r.Platform, r.IPAddress, r.Location, r.VideoSrc)
	if r.Server != ServerJellyfin || r.Title != "The Matrix" || r.UserName != user || r.MediaType != "movie" || r.Decision != "direct_play" {
		t.Errorf("row = %+v", r)
	}
	var ratingKey, sessionKey string
	if err := st.DB().QueryRow(`SELECT rating_key, session_key FROM stream_sessions WHERE id = ?`, r.ID).Scan(&ratingKey, &sessionKey); err != nil {
		t.Fatal(err)
	}
	if ratingKey != matrix.ID || r.UserID != auth.User.ID || len(sessionKey) != len("jellyfin:")+32 {
		t.Errorf("keys = item %q user %q session %q, want %q %q jellyfin:<id>", ratingKey, r.UserID, sessionKey, matrix.ID, auth.User.ID)
	}
	// The media runtime is the clip's ten seconds; the play spans first to last sighting.
	if d := time.Duration(r.DurationMS) * time.Millisecond; d < 9*time.Second || d > 11*time.Second {
		t.Errorf("duration = %v, want the clip's ~10s", d)
	}
	want := lastSeen.Sub(started).Round(time.Second)
	if got := time.Duration(r.StoppedAt-r.StartedAt) * time.Second; got < want-2*time.Second || got > want+time.Second {
		t.Errorf("watched span = %v, want ~%v", got, want)
	}
	if r.ViewOffsetMS != 6000 {
		t.Errorf("last position = %dms, want 6000", r.ViewOffsetMS)
	}
	if r.Player == "" || r.Platform == "" || r.VideoSrc == "" || r.Location != "lan" {
		t.Errorf("player/specs = %q %q %q", r.Player, r.Platform, r.VideoSrc)
	}
	var saved int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM insights_live_sessions`).Scan(&saved); err != nil || saved != 0 {
		t.Errorf("saved streams left = %d, %v", saved, err)
	}
	// The poller listed Jellyfin's people, under Jellyfin.
	users, err := svc.Users(ctx, ServerJellyfin)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range users {
		if u.ID == auth.User.ID {
			found = u.TotalPlays == 1 && u.Username == user
		}
	}
	if !found || len(users) < 2 {
		t.Errorf("jellyfin people = %+v", users)
	}
	// The item's poster goes through the strict proxy path (the synthetic clip may have no
	// image: then Jellyfin's 404 comes back as an error, never anything else).
	if resp, err := svc.Image(ctx, JellyfinItemImage(matrix.ID)); err == nil {
		resp.Body.Close()
		t.Logf("poster: %s", resp.Header.Get("Content-Type"))
	} else {
		t.Logf("poster: %v", err)
	}
	if resp, err := jf.Client(ctx).UserImage(ctx, auth.User.ID); err == nil {
		resp.Body.Close()
		t.Logf("user image: %s", resp.Header.Get("Content-Type"))
	} else {
		t.Logf("user image (none set): %v", err)
	}
}
