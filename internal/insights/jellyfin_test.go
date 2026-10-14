package insights

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/jellyfin"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plex"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// fixtureSessions reads one of internal/jellyfin's captured /Sessions answers (a real 12.2
// server, scripts/jellyfin-it.sh).
func fixtureSessions(t *testing.T, name string) []jellyfin.Session {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "jellyfin", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var out []jellyfin.Session
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func playingOnly(t *testing.T, ss []jellyfin.Session) jellyfin.Session {
	t.Helper()
	for _, s := range ss {
		if s.Playing() {
			return s
		}
	}
	t.Fatal("no playing session in the fixture")
	return jellyfin.Session{}
}

// A direct-play session maps onto the neutral session: GUIDs as keys, the prefixed session
// key, paused state, position and runtime, the source's specs and the bitrate it was given.
func TestJellyfinSessionDirectPlay(t *testing.T) {
	js := playingOnly(t, fixtureSessions(t, "sessions_directplay.json"))
	s, ok := jellyfinSession(js, 2500)
	if !ok {
		t.Fatal("not mapped")
	}
	if s.Server != mediaserver.Jellyfin || s.SessionKey != "jellyfin:"+js.ID || s.RatingKey != js.NowPlayingItem.ID ||
		s.UserID != js.UserID || s.UserName != "ituser" {
		t.Errorf("identity = %+v", s)
	}
	if len(s.RatingKey) != 32 || len(s.UserID) != 32 {
		t.Errorf("Jellyfin keys should be raw 32-hex GUIDs: %q %q", s.RatingKey, s.UserID)
	}
	if s.Type != "movie" || s.Title != "The Matrix" || s.State != "paused" || s.OffsetMS != 4000 || s.DurationMS != 10023 {
		t.Errorf("item/state = type %q title %q state %q offset %d duration %d", s.Type, s.Title, s.State, s.OffsetMS, s.DurationMS)
	}
	if s.Transcoding || s.Decision() != "direct_play" || s.Bandwidth != 2500 || s.SrcBitrate != 2500 {
		t.Errorf("direct play = transcoding %v decision %q bandwidth %d", s.Transcoding, s.Decision(), s.Bandwidth)
	}
	if s.SrcVideoCodec != "h264" || s.SrcVideoBitDepth != 8 || s.SrcAudioCodec != "aac" || s.SrcContainer != "mkv" {
		t.Errorf("source = %q %d-bit audio %q container %q", s.SrcVideoCodec, s.SrcVideoBitDepth, s.SrcAudioCodec, s.SrcContainer)
	}
	if s.Address != "172.21.0.3" || s.PlayerName != "Arrmada" || s.Product != "Arrmada" {
		t.Errorf("player = %q %q %q", s.Address, s.PlayerName, s.Product)
	}
	if s.Thumb != "jellyfin:/Items/"+js.NowPlayingItem.ID+"/Images/Primary" || !IsJellyfinImage(s.Thumb) {
		t.Errorf("thumb = %q", s.Thumb)
	}
}

// A real transcode: the player said DirectPlay, but TranscodingInfo is there, so it's a
// transcode; the bandwidth is the transcode's; software encoding shows no HW.
func TestJellyfinSessionTranscode(t *testing.T) {
	js := playingOnly(t, fixtureSessions(t, "sessions_transcode.json"))
	if js.PlayState.PlayMethod != "DirectPlay" || js.TranscodingInfo == nil {
		t.Fatalf("fixture changed: method %q, transcoding %v", js.PlayState.PlayMethod, js.TranscodingInfo != nil)
	}
	s, ok := jellyfinSession(js, 900)
	if !ok {
		t.Fatal("not mapped")
	}
	if !s.Transcoding || s.Decision() != "transcode" || s.VideoDecision != "transcode" || s.Bandwidth != 364 || s.SrcBitrate != 900 {
		t.Errorf("transcode = %v %q video %q bandwidth %d src %d", s.Transcoding, s.Decision(), s.VideoDecision, s.Bandwidth, s.SrcBitrate)
	}
	if s.SrcVideoCodec != "hevc" || s.SrcVideoBitDepth != 10 || s.SrcVideoProfile != "main 10" || s.StreamVideoCodec != "h264" || s.TranscodeCont != "ts" {
		t.Errorf("specs = src %q %d %q → %q in %q", s.SrcVideoCodec, s.SrcVideoBitDepth, s.SrcVideoProfile, s.StreamVideoCodec, s.TranscodeCont)
	}
	if s.TranscodeHW || s.HWTitle != "" {
		t.Errorf("software transcode marked HW: %v %q", s.TranscodeHW, s.HWTitle)
	}

	// The configured hardware is a badge, never Plex's "fell back to CPU".
	js.TranscodingInfo.HardwareAccelerationType = "vaapi"
	js.TranscodingInfo.IsVideoDirect, js.TranscodingInfo.IsAudioDirect = true, true
	s, _ = jellyfinSession(js, 900)
	if !s.TranscodeHW || s.HWTitle != "vaapi" || s.HWRequested || s.HWEncode || s.Decision() != "direct_stream" {
		t.Errorf("vaapi remux = hw %v %q requested %v encode %v decision %q", s.TranscodeHW, s.HWTitle, s.HWRequested, s.HWEncode, s.Decision())
	}
	ls := &liveSession{sess: s, started: time.Unix(0, 0), lastSeen: time.Unix(60, 0)}
	if rec := ls.record(time.Unix(60, 0)); !rec.HWTranscode || rec.Server != mediaserver.Jellyfin {
		t.Errorf("recorded hw %v server %q", rec.HWTranscode, rec.Server)
	}
}

func TestJellyfinSessionSkips(t *testing.T) {
	js := playingOnly(t, fixtureSessions(t, "sessions_directplay.json"))
	for _, typ := range []string{"AudioBook", "Book", "Photo", "TvChannel"} {
		js.NowPlayingItem.Type = typ
		if _, ok := jellyfinSession(js, 0); ok {
			t.Errorf("%s was recorded", typ)
		}
	}
	js.NowPlayingItem.Type = "Episode"
	js.NowPlayingItem.SeriesName, js.NowPlayingItem.SeriesID = "Breaking Bad", "6d4d5f5c8e2b4a1f9c3e7b2a1d0f9e8c"
	js.NowPlayingItem.ParentIndexNumber, js.NowPlayingItem.IndexNumber = 1, 2
	s, ok := jellyfinSession(js, 0)
	if !ok || s.Type != "episode" || s.ShowTitle != "Breaking Bad" || s.SeasonNum != 1 || s.Index != 2 ||
		s.ShowRatingKey != js.NowPlayingItem.SeriesID || !IsJellyfinImage(s.ShowThumb) || s.SeasonName != "Season 1" {
		t.Errorf("episode = %+v", s)
	}
	if got := clientIP("[::ffff:192.168.1.20]:51234"); got != "192.168.1.20" {
		t.Errorf("clientIP = %q", got)
	}
}

// jfSession is a canned, already-mapped Jellyfin stream.
func jfSession(id, item, title, state string, offsetMS int64) plex.Session {
	return plex.Session{
		Server: mediaserver.Jellyfin, SessionKey: JellyfinSessionKey(id), RatingKey: item, Type: "movie", Title: title,
		UserID: "63273eefab23413faf91bb754c90f24d", UserName: "ituser", State: state, OffsetMS: offsetMS,
		DurationMS: 2 * 3600 * 1000, PlayerName: "Living Room", Platform: "Android TV", Location: "lan", Bandwidth: 8000,
	}
}

const (
	itemDune    = "dbc9717de8df66b09eed7dacda39fcc1"
	itemArrival = "4071f1445812b3d8fba8a91b47dbb5fb"
)

// The recorder follows a Jellyfin stream like a Plex one: a start, a pause, a new item in
// the same session (autoplay) that splits it, and the end.
func TestReconcileJellyfin(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	t0 := time.Now().Add(-2 * time.Minute).Truncate(time.Second) // Watching reads the wall clock
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

	svc.reconcile(ctx, svc.jf, []plex.Session{jfSession("a", itemDune, "Dune", "playing", 0)}, at(0))
	svc.reconcile(ctx, svc.jf, []plex.Session{jfSession("a", itemDune, "Dune", "playing", 10_000)}, at(10))
	if !svc.Watching() {
		t.Error("a playing Jellyfin stream isn't 'watching'")
	}
	svc.reconcile(ctx, svc.jf, []plex.Session{jfSession("a", itemDune, "Dune", "paused", 20_000)}, at(20))
	svc.reconcile(ctx, svc.jf, []plex.Session{jfSession("a", itemDune, "Dune", "paused", 20_000)}, at(50))
	if svc.Watching() {
		t.Error("a paused Jellyfin stream counts as watching")
	}
	// Same session, another item: Dune ends at its last sighting, Arrival starts.
	svc.reconcile(ctx, svc.jf, []plex.Session{jfSession("a", itemArrival, "Arrival", "playing", 0)}, at(60))
	svc.reconcile(ctx, svc.jf, []plex.Session{jfSession("a", itemArrival, "Arrival", "playing", 30_000)}, at(90))
	svc.reconcile(ctx, svc.jf, nil, at(100)) // vanished

	rows, _, err := svc.repo.history(ctx, HistoryFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	arrival, dune := rows[0], rows[1]
	if dune.Title != "Dune" || dune.Server != ServerJellyfin || dune.StoppedAt-dune.StartedAt != 50 || dune.PausedMS != 30_000 {
		t.Errorf("dune = %+v", dune)
	}
	if arrival.Title != "Arrival" || arrival.Server != ServerJellyfin || arrival.StoppedAt-arrival.StartedAt != 30 || arrival.BufferCount != 0 {
		t.Errorf("arrival = %+v", arrival)
	}
	if len(svc.jf.live) != 0 || len(svc.plex.live) != 0 {
		t.Errorf("live left: jellyfin %d plex %d", len(svc.jf.live), len(svc.plex.live))
	}
	// The person is filed under Jellyfin.
	users, err := svc.Users(ctx, ServerJellyfin)
	if err != nil || len(users) != 1 || users[0].Server != ServerJellyfin || users[0].TotalPlays != 2 {
		t.Fatalf("jellyfin users = %+v, %v", users, err)
	}
	if plexUsers, _ := svc.Users(ctx, ServerPlex); len(plexUsers) != 0 {
		t.Errorf("plex users = %+v", plexUsers)
	}
	// Bandwidth samples carry their server.
	var n int
	if err := svc.repo.db.QueryRow(`SELECT COUNT(*) FROM bandwidth_samples WHERE server = 'jellyfin'`).Scan(&n); err != nil || n == 0 {
		t.Errorf("jellyfin bandwidth samples = %d, %v", n, err)
	}
}

// Each server's tracker is its own: a Plex poll that sees nothing doesn't end a Jellyfin
// stream, and turning Plex monitoring off flushes only Plex's.
func TestTrackersAreSeparate(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	t0 := time.Now().Add(-time.Minute).Truncate(time.Second)
	plexSess := plex.Session{SessionKey: "7", RatingKey: "100", Type: "movie", Title: "Heat", UserID: "42", UserName: "sam", State: "playing"}
	svc.reconcile(ctx, svc.jf, []plex.Session{jfSession("a", itemDune, "Dune", "playing", 0)}, t0)
	svc.reconcile(ctx, svc.plex, []plex.Session{plexSess}, t0)
	svc.reconcile(ctx, svc.plex, nil, t0.Add(10*time.Second))
	if len(svc.jf.live) != 1 {
		t.Fatalf("a Plex poll ended the Jellyfin stream")
	}
	if !svc.Watching() {
		t.Error("Watching should follow Jellyfin when Plex has nothing")
	}
	svc.flushAll(ctx, svc.plex)
	if len(svc.jf.live) != 1 {
		t.Fatal("flushing Plex flushed Jellyfin")
	}
	// Only Plex was watching: Watching follows Plex alone too.
	svc2 := newTestService(t)
	svc2.reconcile(ctx, svc2.plex, []plex.Session{plexSess}, time.Now())
	if !svc2.Watching() {
		t.Error("Watching should follow Plex")
	}
	svc2.reconcile(ctx, svc2.plex, nil, time.Now())
	if svc2.Watching() {
		t.Error("nothing is playing")
	}
}

// A restart in the middle of a Jellyfin play continues it (one play, one "Now playing"),
// and a Plex restart's saved streams aren't loaded into Jellyfin's tracker or the reverse.
func TestJellyfinResumeAfterRestart(t *testing.T) {
	ctx := context.Background()
	rig := newRestartRig(t)
	jfStarted, cancel := rig.bus.Subscribe("plex.stream.started")
	t.Cleanup(cancel)
	t0 := time.Unix(1_760_000_000, 0)
	s1 := rig.boot()
	s1.reconcile(ctx, s1.jf, []plex.Session{jfSession("a", itemDune, "Dune", "playing", 0)}, t0)
	s1.reconcile(ctx, s1.jf, []plex.Session{jfSession("a", itemDune, "Dune", "playing", 60_000)}, t0.Add(time.Minute))

	s2 := rig.boot() // restores Plex's tracker only
	if len(s2.plex.live) != 0 {
		t.Fatalf("a Jellyfin stream was restored into Plex's tracker")
	}
	s2.restoreLive(ctx, s2.jf)
	if len(s2.jf.live) != 1 {
		t.Fatalf("jellyfin restored = %d", len(s2.jf.live))
	}
	s2.reconcile(ctx, s2.jf, []plex.Session{jfSession("a", itemDune, "Dune", "playing", 180_000)}, t0.Add(3*time.Minute))
	s2.reconcile(ctx, s2.jf, nil, t0.Add(4*time.Minute))

	rows := rig.rows()
	if len(rows) != 1 || rows[0].Server != ServerJellyfin || rows[0].StoppedAt-rows[0].StartedAt != 180 {
		t.Fatalf("rows = %+v", rows)
	}
	events := 0
	for {
		select {
		case ev := <-jfStarted:
			events++
			if d, _ := ev.Data.(map[string]any); d["server"] != "jellyfin" {
				t.Errorf("event payload server = %v", d["server"])
			}
			continue
		default:
		}
		break
	}
	if events != 1 {
		t.Errorf("Now playing events = %d, want 1", events)
	}
}

// Reliability leaves Jellyfin plays out of every rate and says how many it left out.
func TestReliabilityExcludesJellyfin(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	now := time.Now().Unix()
	for i, rec := range []sessionRecord{
		{SessionKey: "1", UserID: "7", UserName: "sam", RatingKey: "100", MediaType: "movie", Title: "Heat", Platform: "Roku", StartedAt: now - 3600, StoppedAt: now - 60, BufferCount: 1},
		{SessionKey: "2", UserID: "7", UserName: "sam", RatingKey: "101", MediaType: "movie", Title: "Ronin", Platform: "Roku", StartedAt: now - 3000, StoppedAt: now - 60},
		{SessionKey: "jellyfin:a", UserID: "63273eefab23413faf91bb754c90f24d", RatingKey: itemDune, MediaType: "movie", Title: "Dune", StartedAt: now - 2000, StoppedAt: now - 60, Server: mediaserver.Jellyfin},
		{SessionKey: "jellyfin:b", UserID: "63273eefab23413faf91bb754c90f24d", RatingKey: itemArrival, MediaType: "movie", Title: "Arrival", StartedAt: now - 1000, StoppedAt: now - 60, Server: mediaserver.Jellyfin},
	} {
		if _, err := s.repo.insertSession(ctx, rec); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	r, err := s.Reliability(ctx, 7, ServerAll)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.TotalSessions != 2 || r.Summary.BufferedSessions != 1 || r.Summary.BufferRatePct != 50 || r.JellyfinPlays != 2 {
		t.Errorf("all = %+v, jellyfin plays %d", r.Summary, r.JellyfinPlays)
	}
	jr, err := s.Reliability(ctx, 7, ServerJellyfin)
	if err != nil || jr.Summary.TotalSessions != 0 || jr.JellyfinPlays != 2 || jr.Events == nil || jr.ByUser == nil {
		t.Errorf("jellyfin = %+v, %v", jr, err)
	}
	pr, _ := s.Reliability(ctx, 7, ServerPlex)
	if pr.Summary.TotalSessions != 2 || pr.JellyfinPlays != 0 {
		t.Errorf("plex = %+v", pr)
	}

	// The other reads narrow by server and say whose each row is.
	h, err := s.History(ctx, HistoryFilter{Server: ServerJellyfin})
	if err != nil || h.Total != 2 || h.Rows[0].Server != ServerJellyfin {
		t.Errorf("jellyfin history = %+v, %v", h, err)
	}
	g, err := s.History(ctx, HistoryFilter{Server: ServerPlex, Group: true})
	if err != nil || g.Total != 2 || g.Rows[0].Server != ServerPlex {
		t.Errorf("grouped plex history = %+v, %v", g, err)
	}
	st, err := s.Stats(ctx, 7, false, ServerJellyfin)
	if err != nil || len(st.Movies) != 2 || st.Movies[0].Server != ServerJellyfin || len(st.Recent) != 2 {
		t.Errorf("jellyfin stats = %+v, %v", st, err)
	}
	gr, err := s.Graphs(ctx, 7, MetricPlays, ServerPlex)
	if err != nil {
		t.Fatal(err)
	}
	var plays float64
	for _, v := range gr.DailyMovies {
		plays += v
	}
	if plays != 2 {
		t.Errorf("plex graph plays = %v, want 2", plays)
	}
}

// fakeKeys is the API key store.
type fakeKeys map[string]string

func (k fakeKeys) Value(_ context.Context, id string) string     { return k[id] }
func (k fakeKeys) Set(_ context.Context, id, value string) error { k[id] = value; return nil }

// jellyfinRig is a Service wired to a fake Jellyfin answering from the captured fixtures.
type jellyfinRig struct {
	svc      *Service
	jf       *jellyfinsvc.Service
	srv      *httptest.Server
	sessions atomic.Value // fixture name
	items    atomic.Int32 // /Items calls
}

func newJellyfinRig(t *testing.T) *jellyfinRig {
	t.Helper()
	r := &jellyfinRig{svc: newTestService(t)}
	r.sessions.Store("sessions_directplay.json")
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.Contains(req.Header.Get("Authorization"), `Token="jf-key"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch req.URL.Path {
		case "/Sessions":
			b, _ := os.ReadFile(filepath.Join("..", "jellyfin", "testdata", r.sessions.Load().(string)))
			_, _ = w.Write(b)
		case "/Items":
			r.items.Add(1)
			id := req.URL.Query().Get("ids")
			_ = json.NewEncoder(w).Encode(map[string]any{"TotalRecordCount": 1, "Items": []map[string]any{{
				"Id": id, "MediaSources": []map[string]any{{"Id": "other", "Bitrate": 1_000_000}, {"Id": id, "Bitrate": 2_500_000}},
			}}})
		case "/Items/" + itemDune + "/Images/Primary":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	set := settings.NewService(r.svc.repo.db)
	r.svc.settings = set
	keys := fakeKeys{apikeys.Jellyfin: "jf-key"}
	r.jf = jellyfinsvc.New(jellyfinsvc.Options{Settings: set, Keys: keys, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := set.Set(context.Background(), jellyfinsvc.KeyURL, r.srv.URL); err != nil {
		t.Fatal(err)
	}
	r.svc.SetJellyfin(r.jf)
	return r
}

// The Activity view reads Jellyfin through the source: the playing version's bitrate from
// its MediaSources (asked once), LAN from the address, the server on every stream; and a
// Plex that isn't connected just isn't asked.
func TestJellyfinActivity(t *testing.T) {
	ctx := context.Background()
	r := newJellyfinRig(t)
	act, err := r.svc.Activity(ctx, ServerAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(act.Streams) != 1 || len(act.Errors) != 0 {
		t.Fatalf("streams = %+v errors %v", act.Streams, act.Errors)
	}
	st := act.Streams[0]
	if st.Server != ServerJellyfin || st.Bandwidth != 2500 || st.Decision != "direct_play" || st.State != "paused" || st.UserID != "63273eefab23413faf91bb754c90f24d" {
		t.Errorf("stream = %+v", st)
	}
	if st.Location != "lan" || !st.Geo.Local {
		t.Errorf("location = %q local %v", st.Location, st.Geo.Local)
	}
	if !strings.HasPrefix(st.Thumb, "/api/v1/insights/image?path=jellyfin%3A%2FItems%2F") {
		t.Errorf("thumb = %q", st.Thumb)
	}
	if _, err := r.svc.Activity(ctx, ServerAll); err != nil || r.items.Load() != 1 {
		t.Errorf("the source bitrate was asked %d times (err %v), want once", r.items.Load(), err)
	}
	if _, err := r.svc.Activity(ctx, ServerPlex); err == nil {
		t.Error("Plex-only activity without Plex should be 'not configured'")
	}

	// The transcode: its bandwidth is the transcode's.
	r.sessions.Store("sessions_transcode.json")
	act, _ = r.svc.Activity(ctx, ServerJellyfin)
	if len(act.Streams) != 1 || act.Streams[0].Decision != "transcode" || act.Streams[0].Bandwidth != 364 {
		t.Errorf("transcode stream = %+v", act.Streams)
	}

	// Watching / monitoring follow the switch.
	if r.svc.MonitoringActive(ctx) {
		t.Error("monitoring active before the switch")
	}
	on := true
	if err := r.svc.SetJellyfinMonitoring(ctx, &on, nil); err != nil {
		t.Fatal(err)
	}
	if !r.svc.MonitoringActive(ctx) {
		t.Error("monitoring not active after the switch")
	}
	srv := r.svc.Servers(ctx)
	if len(srv) != 2 || srv[1].Server != ServerJellyfin || !srv[1].Configured || !srv[1].Monitoring || srv[1].Status != StatusRecording {
		t.Errorf("servers = %+v", srv)
	}

	// The image proxy path: the strict form fetches, anything else is refused.
	resp, err := r.svc.Image(ctx, JellyfinItemImage(itemDune))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "jpeg" {
		t.Errorf("image body = %q", b)
	}
	for _, bad := range []string{
		"jellyfin:/Items/" + itemDune + "/Images/Backdrop",
		"jellyfin:/Items/" + itemDune + "/Images/Primary?api_key=x",
		"jellyfin:/Items/../System/Info",
		"jellyfin:/Items/" + strings.ToUpper(itemDune) + "/Images/Primary",
		"jellyfin:/System/Info",
		"jellyfin:/Items/" + itemDune + "/Images/Primary/../../../System/Info",
	} {
		if IsJellyfinImage(bad) {
			t.Errorf("%q accepted", bad)
		}
		if _, err := r.svc.Image(ctx, bad); err == nil {
			t.Errorf("%q fetched", bad)
		}
	}
}

// The Jellyfin poll loop records a play end to end and records each poll in Jellyfin's
// health, which it tells about its polling.
func TestRunJellyfin(t *testing.T) {
	r := newJellyfinRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	on, poll := true, 2
	if err := r.svc.SetJellyfinMonitoring(ctx, &on, &poll); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { r.svc.RunJellyfin(ctx); close(done) }()
	// A poll records its health before it folds the sessions in and saves them, so wait for
	// the saved stream itself — waiting on health alone raced the save on slow CI runners.
	saved := func() (string, error) {
		var server string
		err := r.svc.repo.db.QueryRow(`SELECT server FROM insights_live_sessions`).Scan(&server)
		return server, err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := saved(); err == nil && !r.jf.Health(ctx).LastOKAt.IsZero() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if h := r.jf.Health(ctx); h.LastOKAt.IsZero() || !h.Monitoring {
		t.Fatalf("health after a poll = %+v", h)
	}
	cancel()
	<-done
	if server, err := saved(); err != nil || server != ServerJellyfin {
		t.Errorf("saved stream server = %q, %v", server, err)
	}
}
