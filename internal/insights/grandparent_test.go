package insights

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/plex"
)

// imagePathOf undoes proxyImage, so a test can say which Plex image a URL points at.
func imagePathOf(t *testing.T, proxied string) string {
	t.Helper()
	if proxied == "" {
		return ""
	}
	u, err := url.Parse(proxied)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("path")
}

// A live-recorded episode stores its show's poster and rating key, and Most watched TV
// shows that poster — not the episode's landscape still — while older rows without one
// keep their thumb.
func TestTopShowsPreferGrandparentThumb(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	now := time.Now().Unix()
	ins := func(rec sessionRecord) {
		t.Helper()
		if _, err := s.repo.insertSession(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	// Severance: two live plays carrying the show poster.
	for i := 0; i < 2; i++ {
		ins(sessionRecord{UserID: "7", RatingKey: "8812", MediaType: "episode", Title: "Pilot", GrandparentTitle: "Severance",
			Thumb: "/library/metadata/8812/thumb/1", GrandparentThumb: "/library/metadata/8800/thumb/1", GrandparentRatingKey: "8800",
			StartedAt: now - int64(3600*(i+1)), StoppedAt: now - int64(3600*(i+1)) + 1800})
	}
	// An older show recorded before PLEX-20: only the episode thumb.
	ins(sessionRecord{UserID: "7", RatingKey: "9912", MediaType: "episode", Title: "Ep", GrandparentTitle: "Old Show",
		Thumb: "/library/metadata/9912/thumb/1", StartedAt: now - 600, StoppedAt: now - 60})

	st, err := s.Stats(ctx, 30, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Shows) != 2 {
		t.Fatalf("shows = %+v", st.Shows)
	}
	if got := imagePathOf(t, st.Shows[0].ThumbURL); st.Shows[0].Title != "Severance" || got != "/library/metadata/8800/thumb/1" {
		t.Errorf("top show = %q with %q, want Severance with its poster", st.Shows[0].Title, got)
	}
	if got := imagePathOf(t, st.Shows[1].ThumbURL); got != "/library/metadata/9912/thumb/1" {
		t.Errorf("a show without a recorded poster keeps its thumb, got %q", got)
	}

	// History lists the show poster for the episode, too.
	h, err := s.History(ctx, HistoryFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range h.Rows {
		if r.GrandparentTitle == "Severance" && imagePathOf(t, r.ThumbURL) != "/library/metadata/8800/thumb/1" {
			t.Errorf("history row thumb = %q, want the show poster", r.ThumbURL)
		}
	}
}

// The poller writes the show's poster and rating key with the play it records.
func TestRecordStoresGrandparentFields(t *testing.T) {
	ls := &liveSession{started: time.Unix(1_700_000_000, 0), sess: plex.Session{
		SessionKey: "51", RatingKey: "8812", Type: "episode", Title: "Pilot", ShowTitle: "Severance",
		Thumb: "/library/metadata/8812/thumb/1", ShowThumb: "/library/metadata/8800/thumb/1", ShowRatingKey: "8800",
	}}
	rec := ls.record(time.Unix(1_700_000_600, 0))
	if rec.GrandparentThumb != "/library/metadata/8800/thumb/1" || rec.GrandparentRatingKey != "8800" {
		t.Errorf("record = %+v", rec)
	}
}

// A Tautulli import keeps the show's rating key (and poster, where Tautulli lists one), so
// imported and live episodes of one show group together by Plex identity.
func TestImportMapsGrandparentRatingKey(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	c := s.ImportHistory(ctx, []ImportedSession{{
		UserID: 7, UserName: "Sam", RatingKey: "8812", MediaType: "episode", Title: "Pilot", GrandparentTitle: "Severance",
		Thumb: "/library/metadata/8801/thumb/1", GrandparentThumb: "/library/metadata/8800/thumb/1", GrandparentRatingKey: "8800",
		StartedAt: 1_700_000_000, StoppedAt: 1_700_001_800, WatchedMS: 1_700_000,
	}}, ImportOptions{})
	if c.Imported != 1 {
		t.Fatalf("counts = %+v", c)
	}
	rows, _, err := s.repo.history(ctx, HistoryFilter{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].GrandparentRatingKey != "8800" || rows[0].GrandparentThumb != "/library/metadata/8800/thumb/1" {
		t.Fatalf("rows = %+v", rows)
	}
}

// The Activity card shows the show poster for an episode, with the episode still and the
// art alongside; a movie keeps its own poster and has no still.
func TestEnrichEpisodeImages(t *testing.T) {
	s := &Service{}
	ep := s.enrich(plex.Session{Type: "episode", Thumb: "/library/metadata/8812/thumb/1",
		ShowThumb: "/library/metadata/8800/thumb/1", Art: "/library/metadata/8800/art/1"})
	if imagePathOf(t, ep.Thumb) != "/library/metadata/8800/thumb/1" || imagePathOf(t, ep.Still) != "/library/metadata/8812/thumb/1" ||
		imagePathOf(t, ep.Art) != "/library/metadata/8800/art/1" {
		t.Errorf("episode stream = thumb %q still %q art %q", ep.Thumb, ep.Still, ep.Art)
	}
	mv := s.enrich(plex.Session{Type: "movie", Thumb: "/library/metadata/5/thumb/1"})
	if imagePathOf(t, mv.Thumb) != "/library/metadata/5/thumb/1" || mv.Still != "" || !strings.HasPrefix(mv.Thumb, "/api/v1/insights/image") {
		t.Errorf("movie stream = %+v", mv)
	}
}
