package subtitles

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The Series detail page's per-episode chips read GET /subtitles/library?media=tv&series=ID:
// every kept language of every episode on disk, with why a missing one is parked and when
// it is tried again (SUB-29).
func TestSeriesEpisodesIncludesWantStatus(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	db := st.DB()
	set := settings.NewService(db)
	if err := set.Set(ctx, keyLanguages, "en,es"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ep1, ep2 := filepath.Join(dir, "Show - S01E01.mkv"), filepath.Join(dir, "Show - S01E02.mkv")
	for _, p := range []string{ep1, ep2, filepath.Join(dir, "Show - S01E01.en.srt"), filepath.Join(dir, "Show - S01E01.es.srt"), filepath.Join(dir, "Show - S01E02.en.srt")} {
		if err := os.WriteFile(p, []byte(fakeSRT), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO series (id, tmdb_id, title, monitored) VALUES (1, 1, 'Show', 1)`,
		`INSERT INTO seasons (series_id, season_number, monitored) VALUES (1, 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range []string{ep1, ep2} {
		if _, err := db.ExecContext(ctx, `INSERT INTO episodes (series_id, season_number, episode_number, air_date, monitored, has_file, file_path)
			VALUES (1, 1, ?, '2001-01-01', 1, 1, ?)`, i+1, p); err != nil {
			t.Fatal(err)
		}
	}
	// A third episode without a file is left out.
	if _, err := db.ExecContext(ctx, `INSERT INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (1, 1, 3, '2001-01-01', 1)`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Service{settings: set, log: log, ledger: NewLedger(db), series: series.NewService(db, nil, t.TempDir(), log), ai: &fakeAI{}}
	s.probe = func(context.Context, string) (*mediaInfo, error) { return englishAudio(), nil }
	next := time.Now().Add(6 * 24 * time.Hour).Unix()
	if err := s.ledger.SaveBackoff(ctx, Backoff{MediaKey: "e:1:1:2", Lang: "es", VideoPath: ep2, PrimaryReason: ResultNoMatch,
		Detail: "no OpenSubtitles match", Count: 1, NextRetryAt: next}); err != nil {
		t.Fatal(err)
	}

	list, err := s.SeriesEpisodes(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Season    int `json:"season"`
		Episode   int `json:"episode"`
		Missing   int `json:"missing"`
		Languages []struct {
			Lang        string `json:"lang"`
			Have        bool   `json:"have"`
			Reason      string `json:"reason"`
			NextRetryAt int64  `json:"next_retry_at"`
		} `json:"languages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("episodes = %s, want the two with files", raw)
	}
	if got[0].Episode != 1 || got[0].Missing != 0 || len(got[0].Languages) != 2 || !got[0].Languages[0].Have || !got[0].Languages[1].Have {
		t.Errorf("E01 = %+v, want en and es present", got[0])
	}
	e2 := got[1]
	if e2.Episode != 2 || e2.Missing != 1 || len(e2.Languages) != 2 || !e2.Languages[0].Have {
		t.Fatalf("E02 = %+v", e2)
	}
	if es := e2.Languages[1]; es.Lang != "es" || es.Have || es.Reason != string(ResultNoMatch) || es.NextRetryAt != next {
		t.Errorf("E02 es = %+v, want missing, no_match, next try %d", es, next)
	}
}
