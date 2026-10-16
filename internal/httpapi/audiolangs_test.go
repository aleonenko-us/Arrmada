package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/convert"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

func audioAPI(t *testing.T) (*api, context.Context) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	mv := movies.NewService(db, nil, nil, dir, "", nil, log)
	cv := convert.NewService(db, mv, nil, settings.NewService(db), filepath.Join(dir, "no-ffmpeg"), filepath.Join(dir, "no-ffprobe"),
		filepath.Join(dir, "scratch"), filepath.Join(dir, "recycle"), log)
	return &api{deps: Deps{Store: st, Log: log, Movies: mv, Quality: quality.NewService(db), Convert: cv}}, context.Background()
}

func execAll(t *testing.T, a *api, ctx context.Context, stmts ...[]any) {
	t.Helper()
	for _, q := range stmts {
		if _, err := a.deps.Store.DB().ExecContext(ctx, q[0].(string), q[1:]...); err != nil {
			t.Fatal(err)
		}
	}
}

// The movie detail carries each track's audio languages from the library index — the
// original language first, a detected track marked — and their fit against the track's
// profile's language rules; a track without an analysis carries none.
func TestMovieDetailAudioLangs(t *testing.T) {
	a, ctx := audioAPI(t)
	sp, err := a.deps.Quality.Create(ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "Deutsch",
		Ideal: &quality.IdealFile{Language: map[string]string{"de": quality.PrefMust}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + itoa64(sp.ID)
	a.deps.Convert.SetDetectedAudio(func(context.Context, map[string]int64) map[string]map[int]string {
		return map[string]map[int]string{"/m/Film.mkv": {2: "fr"}}
	})
	info := `{"video_codec":"h264","resolution":"1080p","size_bytes":1000,"audio":[` +
		`{"aud_index":0,"codec":"ac3","lang":"ger"},{"aud_index":1,"codec":"ac3","lang":"eng"},` +
		`{"aud_index":2,"codec":"ac3","lang":"und"},{"aud_index":3,"codec":"ac3","lang":"eng","commentary":true}]}`
	execAll(t, a, ctx,
		[]any{`INSERT INTO movies (id, tmdb_id, title, has_file, movie_file_path, quality_profile, extra_json) VALUES (1, 1, 'Film', 1, '/m/Film.mkv', ?, '{"original_language":"en"}')`, ref},
		[]any{`INSERT INTO movie_versions (id, movie_id, label, has_file, file_path, quality_profile) VALUES (2, 1, '4K', 1, '/m/Film - 4K.mkv', ?)`, ref},
		[]any{`INSERT INTO convert_library (path, media_type, movie_id, version_id, version_label, title, size_bytes, video_codec, info_json, info_ver)
		  VALUES ('/m/Film.mkv', 'movie', 1, 0, '', 'Film', 1000, 'h264', ?, 4)`, info},
	)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/movies/{id}", a.handleGetMovie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/movies/1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Versions []struct {
			ID         int64 `json:"id"`
			AudioLangs []struct {
				Code     string `json:"code"`
				Name     string `json:"name"`
				Detected bool   `json:"detected"`
			} `json:"audio_langs"`
			AudioLangFit *struct{ Status, Note string } `json:"audio_lang_fit"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Versions) != 2 {
		t.Fatalf("versions %d", len(out.Versions))
	}
	def := out.Versions[0]
	var codes []string
	for _, l := range def.AudioLangs {
		codes = append(codes, l.Code)
	}
	if strings.Join(codes, ",") != "en,de,fr" || !def.AudioLangs[2].Detected || def.AudioLangs[1].Name != "German" {
		t.Errorf("default track badges %+v", def.AudioLangs)
	}
	if def.AudioLangFit == nil || def.AudioLangFit.Status != "good" {
		t.Errorf("default track fit %+v", def.AudioLangFit)
	}
	if v := out.Versions[1]; v.AudioLangs != nil || v.AudioLangFit != nil {
		t.Errorf("an unanalysed track carries badges: %+v", v)
	}
	if !strings.Contains(rec.Body.String(), `"audio_langs":[{"code":"en","name":"English"}`) {
		t.Errorf("shape: %s", rec.Body.String())
	}
}

// The series detail carries each episode file's languages; an episode whose recorded file
// is no longer the analysed one (a different size) carries none.
func TestSeriesDetailAudioLangs(t *testing.T) {
	a, ctx := audioAPI(t)
	tagged := `{"video_codec":"h264","audio":[{"aud_index":0,"codec":"aac","lang":"jpn"},{"aud_index":1,"codec":"aac","lang":"eng"}]}`
	untagged := `{"video_codec":"h264","audio":[{"aud_index":0,"codec":"aac"}]}`
	execAll(t, a, ctx,
		[]any{`INSERT INTO convert_library (path, media_type, series_id, season, episode, title, size_bytes, video_codec, info_json, info_ver)
		  VALUES ('/tv/S01E01.mkv', 'episode', 7, 1, 1, 'Show', 100, 'h264', ?, 4),
		         ('/tv/S01E02.mkv', 'episode', 7, 1, 2, 'Show', 200, 'h264', ?, 4),
		         ('/tv/S01E03.mkv', 'episode', 7, 1, 3, 'Show', 300, 'h264', ?, 4)`, tagged, untagged, tagged},
	)
	s := series.Series{ID: 7, Extra: &series.SeriesExtra{OriginalLanguage: "ja"}, Seasons: []series.Season{{SeasonNumber: 1, Episodes: []series.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, HasFile: true, FilePath: "/tv/S01E01.mkv", SizeBytes: 100},
		{SeasonNumber: 1, EpisodeNumber: 2, HasFile: true, FilePath: "/tv/S01E02.mkv", SizeBytes: 200},
		{SeasonNumber: 1, EpisodeNumber: 3, HasFile: true, FilePath: "/tv/S01E03.mkv", SizeBytes: 999},
		{SeasonNumber: 1, EpisodeNumber: 4},
	}}}}
	a.attachSeriesAudio(ctx, &s)
	eps := s.Seasons[0].Episodes
	if len(eps[0].AudioLangs) != 2 || eps[0].AudioLangs[0].Code != "ja" || eps[0].AudioLangs[1].Code != "en" {
		t.Errorf("E01 %+v", eps[0].AudioLangs)
	}
	if len(eps[1].AudioLangs) != 1 || eps[1].AudioLangs[0].Code != "" {
		t.Errorf("E02 (untagged, not heard) %+v", eps[1].AudioLangs)
	}
	if eps[2].AudioLangs != nil || eps[3].AudioLangs != nil {
		t.Errorf("a resized file or no file carries badges: %+v %+v", eps[2].AudioLangs, eps[3].AudioLangs)
	}
	if eps[0].AudioLangFit != nil {
		t.Errorf("no language rules, no fit: %+v", eps[0].AudioLangFit)
	}
	b, _ := json.Marshal(eps[3])
	if strings.Contains(string(b), "audio_lang") {
		t.Errorf("an episode without data grew fields: %s", b)
	}
}
