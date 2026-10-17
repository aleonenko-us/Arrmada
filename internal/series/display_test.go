package series

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/displaytitle"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// A show's shown title follows Settings → Library → "Show titles as" and its own override,
// and never what matching, the calendar's identity or a refresh decide.
func TestSeriesDisplayTitles(t *testing.T) {
	ctx := context.Background()
	meta := &fakeMeta{d: metadata.SeriesDetails{
		SeriesResult: metadata.SeriesResult{TMDBID: 61068, Title: "Gomorrah", Year: 2014},
		OriginalName: "Gomorra - La serie", OriginalLang: "it",
		OriginCountry: []string{"IT"}, Seasons: listing(2),
	}}
	var langs []string
	s := localSeriesService(t, meta, t.TempDir(), &langs)
	src := &fakeTitles{titles: map[int]*metadata.Titles{61068: {
		Title: "Gomorrah", OriginalTitle: "Gomorra - La serie", OriginalLanguage: "it",
		Translations: []metadata.TranslatedTitle{{Lang: "el", Country: "GR", Title: "Γόμορρα"}},
	}}}
	s.SetTitlesSource(src)
	p := displaytitle.ParsePolicy("", "", "")
	s.SetDisplayPolicy(func() displaytitle.Policy { return p })

	sr, err := s.AddWith(ctx, 61068, "hd", AddOptions{Monitored: true, Preset: PresetAll})
	if err != nil {
		t.Fatal(err)
	}
	if sr.DisplayTitle != "Gomorrah" || src.calls.Load() != 0 {
		t.Fatalf("default: shows %q after %d TMDB calls, want the English title and none", sr.DisplayTitle, src.calls.Load())
	}

	// Italian originals: the stored original title (kept in every script for shows)
	// shows at once, without asking TMDB.
	p = displaytitle.ParsePolicy("english", "", "it")
	if n, err := s.SyncDisplayTitles(ctx); err != nil || n != 1 {
		t.Fatalf("sync changed %d (%v), want 1", n, err)
	}
	if got, _ := s.Get(ctx, sr.ID); got.DisplayTitle != "Gomorra - La serie" || got.Title != "Gomorrah" {
		t.Errorf("shows %q (title %q), want the Italian original", got.DisplayTitle, got.Title)
	}
	eps, err := s.UpcomingEpisodes(ctx, "2019-12-31", "2020-01-02")
	if err != nil || len(eps) == 0 || eps[0].SeriesTitle != "Gomorra - La serie" {
		t.Errorf("calendar: %+v %v, want the shown title", eps, err)
	}
	if sums := s.AcquisitionSummary(ctx); len(sums) == 0 || sums[0].Title != "Gomorra - La serie" {
		t.Errorf("wanted rows: %+v, want the shown title", sums)
	}

	// My language: Greek — read by the throttled backfill.
	p = displaytitle.ParsePolicy("language", "el", "")
	start := time.Now()
	if n, err := s.BackfillLocalTitles(ctx, 20*time.Millisecond); err != nil || n != 1 || src.calls.Load() != 1 {
		t.Fatalf("display backfill filled %d (%v) with %d calls, want 1 and 1", n, err, src.calls.Load())
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Errorf("the backfill didn't keep its pace (%v)", time.Since(start))
	}
	if got, _ := s.Get(ctx, sr.ID); got.DisplayTitle != "Γόμορρα" {
		t.Errorf("Greek: shows %q", got.DisplayTitle)
	}
	if n, _ := s.BackfillLocalTitles(ctx, time.Millisecond); n != 0 || src.calls.Load() != 1 {
		t.Errorf("second pass read %d (%d calls), want nothing", n, src.calls.Load())
	}

	// An override survives a refresh; cleared, the setting rules again.
	if got, err := s.SetTitleOverride(ctx, sr.ID, "Gomorra"); err != nil || got.DisplayTitle != "Gomorra" {
		t.Fatalf("override: %q %v", got.DisplayTitle, err)
	}
	got, _, err := s.Refresh(ctx, sr.ID, RefreshOptions{})
	if err != nil || got.DisplayTitle != "Gomorra" || got.TitleOverride != "Gomorra" || got.Title != "Gomorrah" {
		t.Errorf("after refresh: shown %q, override %q, title %q (%v)", got.DisplayTitle, got.TitleOverride, got.Title, err)
	}
	if got.Extra == nil || got.Extra.DisplayNames == nil || len(got.Extra.DisplayNames.Translations) != 1 {
		t.Errorf("refresh dropped the display names: %+v", got.Extra)
	}
	if got, err := s.SetTitleOverride(ctx, sr.ID, ""); err != nil || got.DisplayTitle != "Γόμορρα" {
		t.Errorf("cleared: %q %v", got.DisplayTitle, err)
	}
	if _, err := s.SetTitleOverride(ctx, sr.ID, "a\tb"); !errors.Is(err, ErrBadOverride) {
		t.Errorf("a tab: %v, want ErrBadOverride", err)
	}

	// Matching never sees the shown title: the English title still routes a release, the
	// shown one doesn't.
	if m, ok, _ := s.MatchRelease(ctx, parser.Parse("Gomorrah.S01E01.1080p.WEB-GRP")); !ok || m.ID != sr.ID {
		t.Error("the canonical title no longer matches")
	}
	if _, ok, _ := s.MatchRelease(ctx, parser.Parse("Gomorra.S01E01.1080p.WEB-GRP")); ok {
		t.Error("the override matched a release")
	}
}
