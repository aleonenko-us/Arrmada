package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/insights"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// The Library tab is staff-only; without Plex it still lists hot spots from the recorded
// plays and says never watched is unavailable.
func TestInsightsLibraryRoute(t *testing.T) {
	s := insightsRouteServer(t)
	if _, err := s.st.DB().ExecContext(context.Background(),
		`INSERT INTO stream_sessions (user_id,rating_key,media_type,title,decision,video_src,video_stream,started_at,stopped_at)
		 VALUES ('7','5','movie','Dune','transcode','HEVC 4K','H264 1080p',strftime('%s','now')-600,strftime('%s','now')-60)`); err != nil {
		t.Fatal(err)
	}
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	if rec := s.do("GET", "/api/v1/insights/library", kid); rec.Code != http.StatusForbidden {
		t.Fatalf("requester: %d, want 403", rec.Code)
	}
	_, boss := s.user(t, "boss@example.com", auth.RoleManager)
	rec := s.do("GET", "/api/v1/insights/library?window=30", boss)
	if rec.Code != http.StatusOK {
		t.Fatalf("manager: %d %s", rec.Code, rec.Body)
	}
	var v insights.LibraryView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.NeverWatchedUnavailable || len(v.HotSpots) != 1 || v.HotSpots[0].TopPair != "HEVC 4K → H264 1080p" || len(v.MostWatchedMovies) != 1 {
		t.Errorf("library = %s", rec.Body)
	}
}

// Titles the library index knows by TMDB id link to their Arrmada page.
func TestLinkLibraryItems(t *testing.T) {
	var a *api
	s := newRouteServer(t, func(d *Deps) {
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, t.TempDir(), "", nil, d.Log)
		d.Insights = insights.NewService(d.Store.DB(), settings.NewService(d.Store.DB()), nil, nil, d.Log)
	})
	a = &api{deps: s.deps}
	res, err := s.st.DB().ExecContext(context.Background(), `INSERT INTO movies (tmdb_id, title, year, monitored) VALUES (438631, 'Dune', 2021, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	in := &insights.LibraryItem{Kind: "movie", Title: "Dune", Media: "movie", TMDB: 438631}
	out := &insights.LibraryItem{Kind: "movie", Title: "Not here", Media: "movie", TMDB: 1}
	show := &insights.LibraryItem{Kind: "show", Title: "Severance", Media: "series", TMDB: 95396} // no Series service: left unlinked
	a.linkLibraryItems(context.Background(), []*insights.LibraryItem{in, out, show})
	if in.ArrmadaID != id || out.ArrmadaID != 0 || show.ArrmadaID != 0 {
		t.Errorf("linked = %d %d %d, want %d 0 0", in.ArrmadaID, out.ArrmadaID, show.ArrmadaID, id)
	}
}
