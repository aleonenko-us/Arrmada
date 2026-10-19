package music

import (
	"context"
	"encoding/json"
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

// P7, end to end through the real MusicBrainz client and its cache: an album that left
// its artist's list but still existed is remembered as "still there" with a cached 200.
// When MusicBrainz later deletes it, the next due departure check gets a fresh 404 and
// flags it (kept, files and all); while MusicBrainz can't be asked, it stays unknown.
func TestDeletedAfterACheckIsFlaggedOnTheNextCheck(t *testing.T) {
	var mu sync.Mutex
	listed := []string{"rg-a", "rg-b"}
	exists := map[string]bool{"rg-a": true, "rg-b": true}
	down := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/ws/2")
		var body any
		switch {
		case p == "/artist/mb-x":
			body = map[string]any{"id": "mb-x", "name": "X"}
		case p == "/release-group" && r.URL.Query().Get("type") == "album|ep":
			var rgs []any
			for _, id := range listed {
				rgs = append(rgs, map[string]any{"id": id, "title": id, "primary-type": "Album", "first-release-date": "2001-01-01"})
			}
			body = map[string]any{"release-groups": rgs}
		case strings.HasPrefix(p, "/release-group/"):
			id := strings.TrimPrefix(p, "/release-group/")
			if !exists[id] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"Not Found"}`))
				return
			}
			body = map[string]any{"id": id, "title": id, "primary-type": "Album"}
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mb := metadata.NewMusicBrainz(st.DB(), func() metadata.MBConfig {
		return metadata.MBConfig{BaseURL: srv.URL, MirrorRate: metadata.MBMaxMirrorRate}
	})
	svc := NewService(st.DB(), mb, slog.Default())
	clock := time.Now()
	svc.now = func() time.Time { return clock }
	ctx := context.Background()
	// The cache's freshness runs on the real clock: age every cached answer instead.
	age := func(d time.Duration) {
		if _, err := st.DB().Exec(`UPDATE music_mb_cache SET checked_at = checked_at - ?`, d.Milliseconds()); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(d)
	}
	album := func(mbid string) Album {
		t.Helper()
		id, _, _ := svc.repo.AlbumIDByMBID(ctx, mbid)
		al, err := svc.repo.GetAlbum(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return al
	}

	a, err := svc.AddArtist(ctx, "mb-x", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// rg-b leaves the list but still exists (credited elsewhere): checked, kept.
	mu.Lock()
	listed = []string{"rg-a"}
	mu.Unlock()
	age(4 * 24 * time.Hour)
	if _, err := svc.RefreshArtist(ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := album("rg-b"); al.StaleSince != "" || al.departureChecked == "" {
		t.Fatalf("after the first check: stale=%q checked=%q, want kept and remembered", al.StaleSince, al.departureChecked)
	}

	// MusicBrainz deletes it. While MusicBrainz can't be asked, it stays unknown.
	mu.Lock()
	exists["rg-b"] = false
	down = true
	mu.Unlock()
	age(departureRecheck + time.Hour)
	if _, err := svc.RefreshArtist(ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := album("rg-b"); al.StaleSince != "" {
		t.Fatal("flagged while MusicBrainz couldn't be asked")
	}
	// MusicBrainz answers again: the due check's fresh 404 flags it, kept as it was.
	mu.Lock()
	down = false
	mu.Unlock()
	if _, err := svc.RefreshArtist(ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := album("rg-b"); al.StaleSince == "" || !al.Monitored {
		t.Errorf("after MusicBrainz deleted it: stale=%q monitored=%v, want flagged and kept", al.StaleSince, al.Monitored)
	}
}
