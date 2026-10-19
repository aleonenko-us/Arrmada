package music

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// flakyProvider answers with albums until it is told to fail (MusicBrainz down) or to
// answer with nothing.
type flakyProvider struct {
	albums []metadata.AlbumResult
	down   bool
	empty  bool
}

func (p *flakyProvider) Available() bool { return true }
func (p *flakyProvider) SearchArtists(context.Context, string) ([]metadata.ArtistResult, error) {
	return nil, nil
}
func (p *flakyProvider) GetArtist(_ context.Context, mbid string) (*metadata.ArtistDetails, error) {
	if p.down {
		return nil, fmt.Errorf("%w: 503", metadata.ErrMusicBrainzUnavailable)
	}
	return &metadata.ArtistDetails{ArtistResult: metadata.ArtistResult{MBID: mbid, Name: "Radiohead"}}, nil
}
func (p *flakyProvider) ArtistAlbums(context.Context, string) ([]metadata.AlbumResult, error) {
	switch {
	case p.down:
		return nil, fmt.Errorf("%w: 503", metadata.ErrMusicBrainzUnavailable)
	case p.empty:
		return nil, nil
	}
	return p.albums, nil
}
func (p *flakyProvider) AlbumTracks(context.Context, string) ([]metadata.TrackResult, error) {
	return nil, nil
}

// The album page's "Held:" is the lowest tier on disk.
func TestGetAlbumHeldTier(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := &flakyProvider{albums: []metadata.AlbumResult{{MBID: "rg1", Title: "OK Computer", Type: "Album"}}}
	s := NewService(st.DB(), p, slog.Default())
	ctx := context.Background()
	a, err := s.AddArtist(ctx, "abc", "", true)
	if err != nil {
		t.Fatal(err)
	}
	id := a.Albums[0].ID
	if err := s.repo.UpsertTracks(ctx, id, []Track{{DiscNumber: 1, TrackNumber: 1, Title: "A"}, {DiscNumber: 1, TrackNumber: 2, Title: "B"}}); err != nil {
		t.Fatal(err)
	}
	tracks, _ := s.Tracks(ctx, id)
	_ = s.MarkTrackImported(ctx, tracks[0].ID, "/m/a.flac", "FLAC", 900, 1, "")
	_ = s.MarkTrackImported(ctx, tracks[1].ID, "/m/b.mp3", "MP3-V0", 245, 1, "")
	al, err := s.GetAlbum(ctx, id)
	if err != nil || al.HeldTier != QualityMP3V0 {
		t.Errorf("held tier = %q (%v), want MP3-V0", al.HeldTier, err)
	}
}

// A refresh while MusicBrainz is down, or answering with nothing, never removes,
// unmonitors or relists an album (Lidarr's delete-on-empty failure).
func TestRefreshNeverDeletesOnBadAnswers(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := &flakyProvider{albums: []metadata.AlbumResult{
		{MBID: "rg1", Title: "OK Computer", Type: "Album", Year: 1997},
		{MBID: "rg2", Title: "Kid A", Type: "Album", Year: 2000},
	}}
	s := NewService(st.DB(), p, slog.Default())
	ctx := context.Background()
	a, err := s.AddArtist(ctx, "abc", "", true)
	if err != nil || len(a.Albums) != 2 {
		t.Fatalf("add: %+v %v", a, err)
	}
	// One album switched off by hand: a refresh must keep that too.
	if err := s.SetAlbumMonitored(ctx, a.Albums[0].ID, false); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]bool {
		albums, err := s.Albums(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, al := range albums {
			out[al.Title] = al.Monitored
		}
		return out
	}
	before := snapshot()

	p.down = true
	if _, err := s.Refresh(ctx, a.ID); err != nil {
		t.Fatalf("refresh while down: %v", err)
	}
	p.down, p.empty = false, true
	if _, err := s.Refresh(ctx, a.ID); err != nil {
		t.Fatalf("refresh on an empty answer: %v", err)
	}
	after := snapshot()
	if len(after) != len(before) {
		t.Fatalf("albums %v → %v", before, after)
	}
	for title, mon := range before {
		if after[title] != mon {
			t.Errorf("%q monitored %v → %v", title, mon, after[title])
		}
	}
	evs, _ := s.Events(ctx, a.ID, 20)
	found := false
	for _, e := range evs {
		if strings.Contains(e.Detail, "kept what we had") {
			found = true
		}
	}
	if !found {
		t.Errorf("history should say MusicBrainz didn't answer: %+v", evs)
	}
}
