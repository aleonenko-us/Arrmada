package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// jsonShape flattens a JSON document into "path: type" lines (arrays as [], their first
// element standing for the rest), so a test can pin the fields a page reads without
// pinning ids and timestamps.
func jsonShape(prefix string, v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		out[prefix] = "object"
		for k, val := range x {
			jsonShape(prefix+"."+k, val, out)
		}
	case []any:
		out[prefix] = "array"
		if len(x) > 0 {
			jsonShape(prefix+"[]", x[0], out)
		}
	case string:
		out[prefix] = "string"
	case float64:
		out[prefix] = "number"
	case bool:
		out[prefix] = "bool"
	case nil:
		out[prefix] = "null"
	}
}

// MUS-29: the album, artist and library responses keep every field the music pages read
// (AlbumDetail, ArtistDetail, Music) with the same types, and add the new model's.
func TestMusicResponseShapes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	authSvc := auth.NewService(st.DB())
	u, err := authSvc.CreateUser(ctx, "manager", "correct-horse-battery", auth.RoleManager, true)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := authSvc.CreateSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	set := settings.NewService(st.DB())
	_ = set.SetBool(ctx, settings.KeyModuleMusic, true)
	h := New(Deps{
		Log: slog.Default(), Store: st, Auth: authSvc, Settings: set,
		Music: music.NewService(st.DB(), metadata.NewMusicBrainz(nil, nil), slog.Default()),
	}).Handler

	repo := music.NewRepo(st.DB())
	a, err := repo.CreateArtist(ctx, music.Artist{MBID: "mb-rh", Name: "Radiohead", Monitored: true, Genres: []string{"rock"}})
	if err != nil {
		t.Fatal(err)
	}
	albumID, err := repo.UpsertAlbum(ctx, music.Album{ArtistID: a.ID, MBID: "rg-okc", Title: "OK Computer", Year: 1997, AlbumType: "Album", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertTracks(ctx, albumID, []music.Track{
		{DiscNumber: 1, TrackNumber: 1, Title: "Airbag", DurationSec: 284},
		{DiscNumber: 2, TrackNumber: 1, Title: "Lull", DurationSec: 120},
	}); err != nil {
		t.Fatal(err)
	}
	tracks, _ := repo.TracksFor(ctx, albumID)
	if err := repo.SetTrackFile(ctx, tracks[0].ID, "/music/Radiohead/OK Computer/01 - Airbag.flac", "FLAC", 900, 1000, "rel"); err != nil {
		t.Fatal(err)
	}

	get := func(path string) map[string]string {
		t.Helper()
		rec := musicGateCall(h, "GET", path, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
		var v any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		jsonShape("", v, out)
		return out
	}
	check := func(name string, got map[string]string, want []string) {
		t.Helper()
		for _, w := range want {
			path, typ, _ := strings.Cut(w, ": ")
			if got[path] != typ {
				t.Errorf("%s: %s is %q, want %s", name, path, got[path], typ)
			}
		}
		if t.Failed() {
			var all []string
			for k, v := range got {
				all = append(all, k+": "+v)
			}
			sort.Strings(all)
			t.Logf("%s shape:\n%s", name, strings.Join(all, "\n"))
		}
	}

	check("album", get(fmt.Sprintf("/api/v1/music/albums/%d", albumID)), []string{
		// What AlbumDetail.tsx has always read.
		".id: number", ".artist_id: number", ".mbid: string", ".title: string", ".year: number",
		".album_type: string", ".monitored: bool", ".track_count: number", ".have_tracks: number",
		".size_bytes: number", ".held_tier: string",
		".tracks: array", ".tracks[].id: number", ".tracks[].album_id: number", ".tracks[].disc_number: number",
		".tracks[].track_number: number", ".tracks[].title: string", ".tracks[].duration_sec: number",
		".tracks[].monitored: bool", ".tracks[].has_file: bool", ".tracks[].file_path: string",
		".tracks[].format: string", ".tracks[].bitrate_kbps: number", ".tracks[].size_bytes: number",
		// The new model's.
		".primary_type: string", ".artist_credit: string", ".target_edition_id: number", ".edition_tracks: number",
		".editions: array", ".editions[].id: number", ".editions[].track_count: number", ".editions[].disc_count: number",
		".editions[].target: bool", ".editions[].source: string",
		".discs: array", ".discs[].position: number", ".discs[].track_count: number",
		".tracks[].lossless: bool", ".tracks[].file_id: number", ".tracks[].tag_state: string",
	})
	check("artist", get(fmt.Sprintf("/api/v1/music/artists/%d", a.ID)), []string{
		".id: number", ".mbid: string", ".name: string", ".monitored: bool", ".quality_profile: string",
		".genres: array", ".in_library: bool",
		".albums: array", ".albums[].id: number", ".albums[].title: string", ".albums[].track_count: number",
		".albums[].have_tracks: number", ".albums[].monitored: bool", ".albums[].edition_tracks: number",
		".stats: object", ".stats.albums: number", ".stats.tracks: number", ".stats.have_tracks: number",
		".stats.size_bytes: number", ".stats.albums_monitored: number", ".stats.albums_complete: number",
		".stats.albums_unlisted: number", ".stats.albums_with_files: number", ".stats.albums_wanted: number",
	})
	// An upgrade note on the album (track 2 has no file) shows on the album, and Resolve
	// closes it once; a second Resolve, or another album's note, is a 404.
	if _, err := st.DB().Exec(`INSERT INTO music_migration_notes (legacy_track_id, album_id, path, kind, note)
	                           VALUES (?, ?, '/music/x.flac', 'shared_path', 'Track 2 recorded the same file as track 1')`, tracks[1].ID, albumID); err != nil {
		t.Fatal(err)
	}
	check("album notes", get(fmt.Sprintf("/api/v1/music/albums/%d", albumID)), []string{
		".notes: array", ".notes[].id: number", ".notes[].kind: string", ".notes[].note: string",
	})
	resolve := func(album int64) int {
		return musicGateCall(h, "POST", fmt.Sprintf("/api/v1/music/albums/%d/notes/1/resolve", album), session).Code
	}
	if code := resolve(albumID + 1); code != http.StatusNotFound {
		t.Errorf("resolving another album's note = %d, want 404", code)
	}
	if code := resolve(albumID); code != http.StatusOK {
		t.Errorf("resolve = %d, want 200", code)
	}
	if code := resolve(albumID); code != http.StatusNotFound {
		t.Errorf("resolving it again = %d, want 404", code)
	}
	if got := get(fmt.Sprintf("/api/v1/music/albums/%d", albumID)); got[".notes"] != "" {
		t.Error("a resolved note still shows on the album")
	}

	check("library", get("/api/v1/music/artists"), []string{
		".artists: array", ".artists[].id: number", ".artists[].name: string", ".artists[].monitored: bool",
		".artists[].stats: object", ".artists[].stats.tracks: number", ".artists[].stats.have_tracks: number",
		".artists[].stats.albums: number", ".artists[].stats.albums_complete: number", ".artists[].stats.albums_wanted: number",
	})
}
