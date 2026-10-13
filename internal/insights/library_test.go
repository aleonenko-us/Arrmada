package insights

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/plex"
)

// testIndex is an empty library index ready for add.
func testIndex() *plexIndex {
	return &plexIndex{builtAt: time.Now(), byKey: map[string]plex.Item{},
		movieByTMDB: map[int]plex.Item{}, movieByIMDB: map[string]plex.Item{},
		showByTMDB: map[int]plex.Item{}, showByTVDB: map[int]plex.Item{}, showByIMDB: map[string]plex.Item{}}
}

// Hot spots group transcoded plays by title (a show by its show), rank by how often they
// transcoded, and carry the usual video pair, the platforms and the buffer cause.
func TestTranscodeHotSpots(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	now := time.Now().Unix()
	ins := func(r sessionRecord) int64 {
		t.Helper()
		id, err := s.repo.insertSession(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Dune: transcoded three times (twice HEVC 4K → H264 1080p), played direct once.
	var buffered int64
	for i, p := range []struct{ src, stream, platform string }{
		{"HEVC 4K", "H264 1080p", "webOS"}, {"HEVC 4K", "H264 1080p", "webOS"}, {"HEVC 4K", "H264 720p", "Roku"},
	} {
		id := ins(sessionRecord{UserID: "1", RatingKey: "5", MediaType: "movie", Title: "Dune", Year: 2021, Thumb: "/library/metadata/5/thumb/1",
			Decision: "transcode", VideoSrc: p.src, VideoStream: p.stream, Platform: p.platform, StartedAt: now - int64(1000*(i+1)), StoppedAt: now - int64(1000*(i+1)) + 600})
		if i == 0 {
			buffered = id
		}
	}
	ins(sessionRecord{UserID: "1", RatingKey: "5", MediaType: "movie", Title: "Dune", Decision: "direct_play", StartedAt: now - 9000, StoppedAt: now - 8000})
	for i := 0; i < 2; i++ {
		if err := s.repo.insertBufferEvent(ctx, buffered, now, 0, 1000, "transcode_cpu", ""); err != nil {
			t.Fatal(err)
		}
	}
	// Severance: two episodes transcoded for their audio only; the show is the hot spot.
	for i, rk := range []string{"8811", "8812"} {
		ins(sessionRecord{UserID: "2", RatingKey: rk, MediaType: "episode", Title: "Ep", GrandparentTitle: "Severance", GrandparentRatingKey: "800",
			GrandparentThumb: "/library/metadata/800/thumb/1", Thumb: "/library/metadata/" + rk + "/thumb/1",
			Decision: "transcode", VideoSrc: "HEVC 1080p", AudioSrc: "TRUEHD", AudioStream: "AAC", Platform: "Chrome",
			StartedAt: now - int64(500*(i+1)), StoppedAt: now - int64(500*(i+1)) + 300})
	}
	// Outside the window: not counted.
	ins(sessionRecord{UserID: "1", RatingKey: "9", MediaType: "movie", Title: "Old", Decision: "transcode", StartedAt: now - 400*86400, StoppedAt: now - 400*86400 + 60})

	hs, err := s.hotSpots(ctx, windowStart(90))
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 2 {
		t.Fatalf("hot spots = %+v", hs)
	}
	dune, sev := hs[0], hs[1]
	if dune.Title != "Dune" || dune.Kind != "movie" || dune.Transcodes != 3 || dune.TotalPlays != 4 || dune.TopPair != "HEVC 4K → H264 1080p" {
		t.Errorf("dune = %+v", dune)
	}
	if len(dune.Platforms) != 2 || dune.Platforms[0] != "webOS" || dune.TopCause != "transcode_cpu" || dune.CauseLabel == "" {
		t.Errorf("dune platforms/cause = %v %q", dune.Platforms, dune.TopCause)
	}
	if sev.Title != "Severance" || sev.Kind != "show" || sev.Transcodes != 2 || sev.RatingKey != "800" || sev.TopPair != "Audio TRUEHD → AAC" ||
		imagePathOf(t, sev.ThumbURL) != "/library/metadata/800/thumb/1" {
		t.Errorf("severance = %+v", sev)
	}
}

// Never watched lists indexed titles added over 30 days ago with no play — matched by
// rating key, or for shows recorded before PLEX-20 by name — oldest first. Without an
// index it says it can't tell.
func TestNeverWatched(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	old := time.Now().Add(-60 * 24 * time.Hour).Unix()
	older := time.Now().Add(-90 * 24 * time.Hour).Unix()
	fresh := time.Now().Add(-5 * 24 * time.Hour).Unix()

	v, err := s.Library(ctx, 90, false)
	if err != nil {
		t.Fatal(err)
	}
	if !v.NeverWatchedUnavailable || len(v.NeverWatched) != 0 {
		t.Fatalf("without an index: %+v", v)
	}

	idx := testIndex()
	for _, it := range []plex.Item{
		{RatingKey: "5", Type: "movie", Title: "Dune", AddedAt: older, TMDB: 438631},        // watched by key
		{RatingKey: "6", Type: "movie", Title: "Heat", AddedAt: old},                        // watched by title (an import)
		{RatingKey: "7", Type: "movie", Title: "Arrival", AddedAt: old, TMDB: 329865},       // never watched
		{RatingKey: "8", Type: "movie", Title: "New Film", AddedAt: fresh},                  // too new to judge
		{RatingKey: "800", Type: "show", Title: "Severance", AddedAt: older},                // watched by show key
		{RatingKey: "900", Type: "show", Title: "The Bear", AddedAt: older},                 // watched by name only
		{RatingKey: "901", Type: "show", Title: "Slow Horses", AddedAt: older, TMDB: 95480}, // never watched
	} {
		idx.add(it)
	}
	s.links.idx = idx
	seedRows(t, s,
		sessionRecord{UserID: "1", RatingKey: "5", MediaType: "movie", Title: "Dune", StartedAt: 100, StoppedAt: 200},
		sessionRecord{UserID: "1", MediaType: "movie", Title: "heat", StartedAt: 100, StoppedAt: 200},
		sessionRecord{UserID: "1", RatingKey: "811", MediaType: "episode", Title: "Ep", GrandparentTitle: "Severance", GrandparentRatingKey: "800", StartedAt: 100, StoppedAt: 200},
		sessionRecord{UserID: "1", RatingKey: "911", MediaType: "episode", Title: "Ep", GrandparentTitle: "The Bear", StartedAt: 100, StoppedAt: 200},
	)
	v, err = s.Library(ctx, 90, false)
	if err != nil {
		t.Fatal(err)
	}
	if v.NeverWatchedUnavailable || v.NeverWatchedTotal != 2 || len(v.NeverWatched) != 2 {
		t.Fatalf("never watched = %+v (total %d)", v.NeverWatched, v.NeverWatchedTotal)
	}
	first, second := v.NeverWatched[0], v.NeverWatched[1]
	if first.Title != "Slow Horses" || first.Kind != "show" || first.Media != "series" || first.TMDB != 95480 || first.AddedAt != older {
		t.Errorf("oldest = %+v", first)
	}
	if second.Title != "Arrival" || second.Media != "movie" || second.TMDB != 329865 {
		t.Errorf("second = %+v", second)
	}
	// Most watched carries the rating key, mapped to TMDB through the index.
	if len(v.MostWatchedMovies) != 0 { // the seeded plays are long outside the window
		t.Errorf("most watched in the window = %+v", v.MostWatchedMovies)
	}
}

// Most watched ranks by plays or by hours and links titles through the index.
func TestLibraryMostWatched(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	idx := testIndex()
	idx.add(plex.Item{RatingKey: "5", Type: "movie", Title: "Dune", TMDB: 438631})
	idx.add(plex.Item{RatingKey: "800", Type: "show", Title: "Severance", TMDB: 95396})
	s.links.idx = idx
	now := time.Now().Unix()
	seedRows(t, s,
		sessionRecord{UserID: "1", RatingKey: "5", MediaType: "movie", Title: "Dune", StartedAt: now - 9000, StoppedAt: now - 8900},
		sessionRecord{UserID: "2", RatingKey: "5", MediaType: "movie", Title: "Dune", StartedAt: now - 8000, StoppedAt: now - 7900},
		sessionRecord{UserID: "1", RatingKey: "6", MediaType: "movie", Title: "Heat", StartedAt: now - 7000, StoppedAt: now - 7000 + 10000},
		sessionRecord{UserID: "1", RatingKey: "811", MediaType: "episode", Title: "Ep", GrandparentTitle: "Severance", GrandparentRatingKey: "800", StartedAt: now - 500, StoppedAt: now - 100},
	)
	v, err := s.Library(ctx, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.MostWatchedMovies) != 2 || v.MostWatchedMovies[0].Title != "Dune" || v.MostWatchedMovies[0].Media != "movie" || v.MostWatchedMovies[0].TMDB != 438631 {
		t.Errorf("by plays = %+v", v.MostWatchedMovies)
	}
	if len(v.MostWatchedShows) != 1 || v.MostWatchedShows[0].Media != "series" || v.MostWatchedShows[0].TMDB != 95396 {
		t.Errorf("shows = %+v", v.MostWatchedShows)
	}
	h, _ := s.Library(ctx, 30, true)
	if h.MostWatchedMovies[0].Title != "Heat" {
		t.Errorf("by hours = %+v", h.MostWatchedMovies)
	}
}
