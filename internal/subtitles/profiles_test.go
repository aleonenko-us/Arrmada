package subtitles

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// profileFixture is a Service over a temp database with real settings, movies and series,
// the kept-language setting at langs ("" = never set), and probe/AI seams.
type profileFixture struct {
	s  *Service
	st *store.Store
	mi map[string]*mediaInfo // probe results by path; missing = no tracks, English audio
}

func newProfileFixture(t *testing.T, langs string) *profileFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.NewService(st.DB())
	if langs != "" {
		if err := set.Set(context.Background(), keyLanguages, langs); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &profileFixture{st: st, mi: map[string]*mediaInfo{}}
	f.s = &Service{
		settings: set, log: log, wake: make(chan struct{}, 1), ledger: NewLedger(st.DB()),
		movies: movies.NewService(st.DB(), nil, nil, t.TempDir(), "", nil, log),
		series: series.NewService(st.DB(), nil, t.TempDir(), log),
		ai:     &fakeAI{avail: true, transcribe: true, translate: true},
	}
	var mu sync.Mutex
	f.s.probe = func(_ context.Context, p string) (*mediaInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		if mi, ok := f.mi[p]; ok {
			return mi, nil
		}
		return englishAudio(), nil
	}
	return f
}

func (f *profileFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.st.DB().Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

// video writes a synthetic video plus the named sidecars in a fresh folder.
func video(t *testing.T, name string, sidecars ...string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sc := range sidecars {
		if err := os.WriteFile(filepath.Join(dir, sc), []byte(fakeSRT), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func (f *profileFixture) movie(t *testing.T, id int64, path string) {
	t.Helper()
	f.exec(t, `INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (?, ?, ?, 1, 1, ?)`,
		id, 1000+id, "Movie "+filepath.Base(path), path)
}

func (f *profileFixture) show(t *testing.T, id int64, paths ...string) {
	t.Helper()
	f.exec(t, `INSERT INTO series (id, tmdb_id, title) VALUES (?, ?, ?)`, id, 2000+id, "Show")
	f.exec(t, `INSERT INTO seasons (series_id, season_number) VALUES (?, 1)`, id)
	for i, p := range paths {
		f.exec(t, `INSERT INTO episodes (series_id, season_number, episode_number, has_file, file_path) VALUES (?, 1, ?, 1, ?)`, id, i+1, p)
	}
}

func items(spec ...string) []ProfileItem {
	var out []ProfileItem
	for _, sp := range spec {
		parts := strings.Split(sp, "/")
		it := ProfileItem{Lang: parts[0]}
		if len(parts) > 1 {
			it.Variant = parts[1]
		}
		if len(parts) > 2 {
			it.SDH = parts[2]
		}
		if len(parts) > 3 && parts[3] == "a" {
			it.OnlyIfAudioDiffers = true
		}
		out = append(out, it)
	}
	return out
}

func (f *profileFixture) setDefault(t *testing.T, its []ProfileItem) {
	t.Helper()
	def := f.s.Profiles(context.Background())[0]
	if _, err := f.s.UpdateProfile(context.Background(), def.ID, nil, its); err != nil {
		t.Fatal(err)
	}
}

// The default profile is seeded from the kept-language setting the first time it's read
// — canonical, in order, one plain item each — so an existing install counts exactly as
// before. An unset setting seeds Go's default.
func TestDefaultProfileSeededFromSetting(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "eng,fre,pt_BR")
	ps := f.s.Profiles(ctx)
	if len(ps) != 1 || !ps[0].IsDefault || ps[0].Name != "Default" {
		t.Fatalf("profiles = %+v, want just the default", ps)
	}
	if want := items("en/full/any", "fr/full/any", "pt-br/full/any"); !reflect.DeepEqual(ps[0].Items, want) {
		t.Errorf("default items = %+v, want %+v", ps[0].Items, want)
	}
	if got := f.s.languages(ctx); !reflect.DeepEqual(got, []string{"en", "fr", "pt-br"}) {
		t.Errorf("languages = %v", got)
	}
	// The key the snapshot compares is the flat list it always was.
	if k := f.s.wantsKey(ctx); k != "en,fr,pt-br" {
		t.Errorf("wantsKey = %q, want the flat list", k)
	}
	// Seeded once: a fresh read (another start) finds the row rather than seeding again.
	f.s.dropProfiles()
	if n := len(f.s.Profiles(ctx)); n != 1 {
		t.Errorf("%d profiles after a reload, want 1", n)
	}

	unset := newProfileFixture(t, "")
	if got := unset.s.Profiles(ctx)[0].Items; !reflect.DeepEqual(got, items("en/full/any")) {
		t.Errorf("unset setting seeded %+v, want [en]", got)
	}
}

// SetSettings(languages) — the Kept languages picker — rewrites the default profile's
// languages, keeping each remaining language's options, and the setting follows.
func TestSetSettingsRewritesDefaultProfile(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en,es")
	f.setDefault(t, items("en/both/prefer/a", "es"))
	if err := f.s.SetSettings(ctx, nil, nil, []string{"fr", "en"}); err != nil {
		t.Fatal(err)
	}
	if got, want := f.s.Profiles(ctx)[0].Items, items("fr/full/any", "en/both/prefer/a"); !reflect.DeepEqual(got, want) {
		t.Errorf("default items = %+v, want %+v", got, want)
	}
	if got := f.s.settings.Get(ctx, keyLanguages, ""); got != "fr,en" {
		t.Errorf("setting = %q, want fr,en", got)
	}
	if got := f.s.languages(ctx); !reflect.DeepEqual(got, []string{"fr", "en"}) {
		t.Errorf("settings languages = %v", got)
	}
}

// A title uses its override, else the default; a series override covers that series'
// episodes only, and never a movie that happens to share the id.
func TestWantsResolution(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en")
	f.movie(t, 1, "/m/1.mkv")
	f.movie(t, 7, "/m/7.mkv")
	f.show(t, 7)
	f.show(t, 8)
	anime, err := f.s.CreateProfile(ctx, "Anime", items("ja", "en/forced"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetOverride(ctx, OverrideSeries, 7, anime.ID); err != nil {
		t.Fatal(err)
	}
	want := func(ws []Want) []string {
		var out []string
		for _, w := range ws {
			out = append(out, wantLabel(w.Lang, w.Variant))
		}
		return out
	}
	for _, c := range []struct {
		kind              string
		movieID, seriesID int64
		want              []string
	}{
		{"episode", 0, 7, []string{"ja", "en forced"}},
		{"episode", 0, 8, []string{"en"}},
		{"movie", 7, 0, []string{"en"}},
		{"movie", 1, 0, []string{"en"}},
	} {
		if got := want(f.s.titleWants(ctx, c.kind, c.movieID, c.seriesID)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s movie=%d series=%d wants %v, want %v", c.kind, c.movieID, c.seriesID, got, c.want)
		}
	}
	if err := f.s.SetOverride(ctx, OverrideMovie, 1, anime.ID); err != nil {
		t.Fatal(err)
	}
	if got := want(f.s.titleWants(ctx, "movie", 1, 0)); !reflect.DeepEqual(got, []string{"ja", "en forced"}) {
		t.Errorf("overridden movie wants %v", got)
	}
	// Pinning to the default is no override; clearing goes back to the default.
	if err := f.s.SetOverride(ctx, OverrideMovie, 1, f.s.Profiles(ctx)[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := len(f.s.Overrides(ctx)); n != 1 {
		t.Errorf("%d overrides after pinning a movie to the default, want 1 (the series)", n)
	}
	// Deleting a profile sends its titles back to the default.
	if err := f.s.DeleteProfile(ctx, anime.ID); err != nil {
		t.Fatal(err)
	}
	if got := want(f.s.titleWants(ctx, "episode", 0, 7)); !reflect.DeepEqual(got, []string{"en"}) {
		t.Errorf("after delete, series 7 wants %v", got)
	}
	if n := len(f.s.Overrides(ctx)); n != 0 {
		t.Errorf("%d overrides left after deleting their profile", n)
	}
}

func TestProfileValidation(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en")
	f.movie(t, 1, "/m/1.mkv")
	if _, err := f.s.CreateProfile(ctx, "  ", items("en")); !errors.Is(err, ErrProfileInvalid) {
		t.Errorf("blank name: %v", err)
	}
	if _, err := f.s.CreateProfile(ctx, "X", nil); !errors.Is(err, ErrProfileInvalid) {
		t.Errorf("no items: %v", err)
	}
	if _, err := f.s.CreateProfile(ctx, "X", items("klingon")); !errors.Is(err, ErrUnknownLanguage) {
		t.Errorf("unknown language: %v", err)
	}
	if _, err := f.s.CreateProfile(ctx, "X", items("en", "eng")); !errors.Is(err, ErrProfileInvalid) {
		t.Errorf("a language twice: %v", err)
	}
	if _, err := f.s.CreateProfile(ctx, "X", items("en/sometimes")); !errors.Is(err, ErrProfileInvalid) {
		t.Errorf("bad variant: %v", err)
	}
	if _, err := f.s.CreateProfile(ctx, "X", items("en/full/maybe")); !errors.Is(err, ErrProfileInvalid) {
		t.Errorf("bad sdh: %v", err)
	}
	p, err := f.s.CreateProfile(ctx, "Kids", items("ENG"))
	if err != nil || p.Items[0].Lang != "en" || p.Items[0].Variant != WantFull || p.Items[0].SDH != SDHAny {
		t.Fatalf("create = %+v, %v (want canonical, full, any)", p, err)
	}
	if _, err := f.s.CreateProfile(ctx, "kids", items("en")); !errors.Is(err, ErrProfileNameTaken) {
		t.Errorf("a taken name (any case): %v", err)
	}
	if _, err := f.s.CreateProfile(ctx, "default", items("en")); !errors.Is(err, ErrProfileNameTaken) {
		t.Errorf("the default's name: %v", err)
	}
	if err := f.s.DeleteProfile(ctx, f.s.Profiles(ctx)[0].ID); !errors.Is(err, ErrDefaultProfile) {
		t.Errorf("deleting the default: %v", err)
	}
	if err := f.s.DeleteProfile(ctx, 9999); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("deleting nothing: %v", err)
	}
	if err := f.s.SetOverride(ctx, "album", 1, p.ID); !errors.Is(err, ErrOverrideKind) {
		t.Errorf("bad kind: %v", err)
	}
	if err := f.s.SetOverride(ctx, OverrideMovie, 42, p.ID); !errors.Is(err, ErrOverrideNoTitle) {
		t.Errorf("no such movie: %v", err)
	}
	if err := f.s.SetOverride(ctx, OverrideMovie, 1, 9999); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("no such profile: %v", err)
	}
}

// 'Only when audio differs': an English-audio film isn't missing English; a French one is;
// one whose audio is unknown still wants it. A forced want ignores the option.
func TestOnlyIfAudioDiffers(t *testing.T) {
	ws := Profile{Items: items("en/both/any/a", "fr")}.wants()
	labels := func(ws []Want) string {
		var out []string
		for _, w := range ws {
			out = append(out, wantLabel(w.Lang, w.Variant))
		}
		return strings.Join(out, ",")
	}
	french := &mediaInfo{Audio: []AudioTrack{{Index: 0, Lang: "fre"}, {Index: 1, Lang: "eng", Default: true}}}
	for _, c := range []struct {
		name string
		mi   *mediaInfo
		want string
	}{
		{"english audio", englishAudio(), "en forced,fr"},
		{"default track is english", french, "en forced,fr"},
		{"french audio", &mediaInfo{Audio: []AudioTrack{{Index: 0, Lang: "fra"}}}, "en,en forced,fr"},
		{"untagged audio", &mediaInfo{Audio: []AudioTrack{{Index: 0, Lang: "und"}}}, "en,en forced,fr"},
		{"untagged, heard as english", &mediaInfo{Audio: []AudioTrack{{Index: 0, Lang: "und"}}, Detected: map[int]DetectedLang{0: {Lang: "en", Prob: 0.9}}}, "en forced,fr"},
		{"untagged, barely heard", &mediaInfo{Audio: []AudioTrack{{Index: 0}}, Detected: map[int]DetectedLang{0: {Lang: "en", Prob: 0.3}}}, "en,en forced,fr"},
		{"never probed", nil, "en,en forced,fr"},
	} {
		if got := labels(activeWants(ws, c.mi)); got != c.want {
			t.Errorf("%s: active wants %s, want %s", c.name, got, c.want)
		}
	}

	// Through the Library: the English film isn't counted as missing English.
	ctx := context.Background()
	f := newProfileFixture(t, "en")
	f.setDefault(t, items("en/full/any/a"))
	eng := video(t, "English (2020).mkv")
	fra := video(t, "Amelie (2001).mkv")
	f.mi[fra] = &mediaInfo{Audio: []AudioTrack{{Index: 0, Lang: "fre"}}, AudioLangs: []string{"fre"}}
	f.movie(t, 1, eng)
	f.movie(t, 2, fra)
	lib, err := f.s.Library(ctx, "movies")
	if err != nil {
		t.Fatal(err)
	}
	missing := map[int64]int{}
	for _, fs := range lib {
		missing[fs.MovieID] = fs.Missing
	}
	if missing[1] != 0 || missing[2] != 1 {
		t.Errorf("missing = %v, want the English film 0 and the French film 1", missing)
	}
	// The sweep agrees once the probe is known; the English film isn't queued.
	if n, err := f.s.SweepMissing(ctx, "movies"); err != nil || n != 1 {
		t.Errorf("sweep queued %d (%v), want just the French film", n, err)
	}
}

// Coverage counts variants: a forced want is met only by a forced sidecar, a full want
// never by one.
func TestCoverageCountsVariants(t *testing.T) {
	full := Profile{Items: items("en")}.wants()
	forced := Profile{Items: items("en/forced")}.wants()
	both := Profile{Items: items("en/both")}.wants()
	for _, c := range []struct {
		name     string
		sidecars []string
		wants    []Want
		missing  int
	}{
		{"full want, full sidecar", []string{"M.en.srt"}, full, 0},
		{"full want, SDH sidecar", []string{"M.en.sdh.srt"}, full, 0},
		{"full want, forced only", []string{"M.en.forced.srt"}, full, 1},
		{"forced want, full only", []string{"M.en.srt"}, forced, 1},
		{"forced want, forced sidecar", []string{"M.en.forced.srt"}, forced, 0},
		{"forced want, untagged forced", []string{"M.forced.srt"}, forced, 1},
		{"both, full only", []string{"M.en.srt"}, both, 1},
		{"both, both there", []string{"M.en.srt", "M.eng.forced.srt"}, both, 0},
		{"both, unpaired forced", []string{"M.en.srt", "Other.en.forced.srt"}, both, 1},
	} {
		p := video(t, "M.mkv", c.sidecars...)
		if got := len(coverageOf(p, "movie", c.wants, nil).missing()); got != c.missing {
			t.Errorf("%s: %d missing, want %d", c.name, got, c.missing)
		}
	}
}

// The default profile of plain items reproduces the flat-list coverage exactly: the same
// missing count and the same sweep decision for every file of a fixture library.
func TestDefaultProfileMatchesFlatCoverage(t *testing.T) {
	langs := []string{"en", "pt-br", "fr"}
	ws := plainWants(langs)
	for _, sidecars := range [][]string{
		{},
		{"M.srt"},
		{"M.en.srt"},
		{"M.eng.srt", "M.fr.forced.srt"},
		{"M.en.hi.srt", "M.pt.srt"},
		{"M.pt-BR.srt", "M.french.srt"},
		{"M.proper.srt"},
		{"Old Name.en.srt"},
		{"Old Name.en.srt", "M.fr.srt"},
		{"M.en.forced.srt", "M.hi.srt"},
	} {
		p := video(t, "M.mkv", sidecars...)
		sc := scanSidecars(p, langs, "movie")
		flatMissing := len(missingOf(langs, sc.Present))
		cov := coverageOf(p, "movie", ws, nil)
		if got := len(cov.missing()); got != flatMissing {
			t.Errorf("%v: profile counts %d missing, flat list %d", sidecars, got, flatMissing)
		}
		var sweep []string
		for _, w := range cov.sweepable() {
			sweep = append(sweep, w.Lang)
		}
		if flat := movieSweepLangs(p, langs); !reflect.DeepEqual(sweep, flat) {
			t.Errorf("%v: profile sweeps %v, flat list %v", sidecars, sweep, flat)
		}
	}
}

// A forced want extracts the forced track to <base>.en.forced.srt and that file is its
// coverage; nothing else is made for it, and the AI is never asked.
func TestForcedWantExtractsForced(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en")
	f.setDefault(t, items("en/forced"))
	p := video(t, "Movie (2020).mkv")
	f.movie(t, 1, p)
	f.mi[p] = &mediaInfo{
		Audio: []AudioTrack{{Index: 0, Lang: "eng"}}, AudioLangs: []string{"eng"},
		Subs: []SubTrack{
			{Index: 0, Lang: "eng", Text: true, Forced: true, Title: "English (Forced)"},
			{Index: 1, Lang: "eng", Text: true, Title: "English"},
		},
	}
	var picked []extractPick
	f.s.extract = func(_ context.Context, _ string, picks []extractPick) error {
		picked = append(picked, picks...)
		for _, pk := range picks {
			if err := os.WriteFile(pk.Path, []byte(fakeSRT), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	job := f.s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Priority: PrioManual})
	f.s.pop()
	f.s.process(ctx, job)
	if job.State != StateDone {
		t.Fatalf("job %s: %s", job.State, job.Note)
	}
	if len(picked) != 1 || picked[0].Index != 0 || picked[0].Variant != VariantForced || filepath.Base(picked[0].Path) != "Movie (2020).en.forced.srt" {
		t.Fatalf("extracted %+v, want only stream 0 to .en.forced.srt", picked)
	}
	if _, err := os.Stat(sidecarPath(p, "en")); err == nil {
		t.Error("a full English subtitle was made for a forced-only want")
	}
	if len(f.s.ai.(*fakeAI).calls) != 0 {
		t.Errorf("AI was asked: %v", f.s.ai.(*fakeAI).calls)
	}
	if len(job.Outcomes) != 1 || job.Outcomes[0].Variant != VariantForced || job.Outcomes[0].Result != ResultOK {
		t.Errorf("outcomes = %+v", job.Outcomes)
	}
	if !strings.Contains(job.Note, "en forced: extracted") {
		t.Errorf("note = %q", job.Note)
	}
	if got := len(coverageOf(p, "movie", f.s.titleWants(ctx, "movie", 1, 0), nil).missing()); got != 0 {
		t.Errorf("%d wants still missing after the run", got)
	}
}

// recordingProvider is a fake OpenSubtitles that records every search request.
type recordingProvider struct {
	fakeProvider
	reqs []SearchRequest
}

func (p *recordingProvider) Search(ctx context.Context, r SearchRequest) ([]SubtitleResult, error) {
	p.reqs = append(p.reqs, r)
	return p.fakeProvider.Search(ctx, r)
}

// A forced want with no forced track searches foreign-parts-only uploads and writes the
// ".forced" file; what nothing can make is recorded as impossible for AI, and it backs off
// as its own variant.
func TestForcedWantDownloadsForeignParts(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en")
	f.setDefault(t, items("en/forced", "fr/forced"))
	p := video(t, "Movie (2020).mkv")
	f.movie(t, 1, p)
	prov := &recordingProvider{fakeProvider: fakeProvider{canDownload: true, results: map[string][]SubtitleResult{
		"en": {{FileID: "9", Language: "en", ForeignPartsOnly: true, HashMatch: true}},
	}}}
	f.s.provider = prov
	f.s.resolve = func(context.Context, *Job) (fileRef, bool) {
		return fileRef{Path: p, Title: "Movie", MediaKey: "m:1"}, true
	}
	job := f.s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Priority: PrioManual})
	f.s.pop()
	f.s.process(ctx, job)
	if job.State != StateDone {
		t.Fatalf("job %s: %s", job.State, job.Note)
	}
	for _, r := range prov.reqs {
		if r.ForeignParts != "only" {
			t.Errorf("search %s asked foreign_parts=%q, want only", r.Language, r.ForeignParts)
		}
	}
	if _, err := os.Stat(sidecarPathV(p, "en", VariantForced)); err != nil {
		t.Errorf("no .en.forced.srt: %v", err)
	}
	if _, err := os.Stat(sidecarPath(p, "en")); err == nil {
		t.Error("the forced download was written as the full subtitle")
	}
	bs, err := f.s.ledger.BackoffsFor(ctx, "m:1")
	if err != nil {
		t.Fatal(err)
	}
	if !bs.BlockedV("m:1", "fr", VariantForced, p, time.Now()) || bs.Blocked("m:1", "fr", p, time.Now()) {
		t.Errorf("backoff = %+v, want fr parked as its forced variant only", bs)
	}
	if !strings.Contains(job.Note, "fr forced:") {
		t.Errorf("note = %q", job.Note)
	}
}

// Without a forced track or downloads, a forced want is impossible for now — parked until
// the languages change or OpenSubtitles is set up, never sent to whisper.
func TestForcedImpossibleWaitsOnCreds(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en")
	f.setDefault(t, items("en/forced"))
	p := video(t, "Movie (2020).mkv")
	f.movie(t, 1, p)
	job := f.s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Priority: PrioManual})
	f.s.pop()
	f.s.process(ctx, job)
	if job.State != StateSkipped || len(f.s.ai.(*fakeAI).calls) != 0 {
		t.Fatalf("job %s (%s), AI calls %v; want skipped without AI", job.State, job.Note, f.s.ai.(*fakeAI).calls)
	}
	bs, err := f.s.ledger.BackoffsFor(ctx, "m:1")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := bs[BackoffKey("m:1", "en", VariantForced)]
	if !ok || b.PrimaryReason != ResultImpossible || !reflect.DeepEqual(b.ClearOn, []string{EventCreds, EventLangs}) {
		t.Errorf("backoff = %+v (found %v), want impossible clearing on creds and langs", b, ok)
	}
}

// With the lanes on, a forced want is settled in the fast lane and only the full subtitle
// goes on to whisper; the GPU part ends the job with both stories and parks the forced
// want as its own variant.
func TestForcedWantStaysInFastLane(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{avail: true, transcribe: true}
	s, videos := laneFixture(t, "en", ai)
	def := s.Profiles(ctx)[0]
	if _, err := s.UpdateProfile(ctx, def.ID, nil, items("en/both")); err != nil {
		t.Fatal(err)
	}
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat", Priority: PrioManual})
	if s.popLane(LaneFast) != job {
		t.Fatal("fast lane didn't hand out the job")
	}
	s.process(ctx, job)
	if job.Lane != LaneGPU || !reflect.DeepEqual(job.Langs, []string{"en"}) {
		t.Fatalf("after the fast lane: lane=%s langs=%v, want the GPU lane for en", job.Lane, job.Langs)
	}
	s.mu.Lock()
	s.releaseLocked(job)
	s.mu.Unlock()
	if s.popLane(LaneGPU) != job {
		t.Fatal("GPU lane didn't hand out the job")
	}
	s.process(ctx, job)
	if job.State != StateDone || !strings.HasPrefix(job.Note, "en: AI-generated · en forced: ") {
		t.Errorf("final: state=%s note=%q", job.State, job.Note)
	}
	if len(ai.calls) != 1 {
		t.Errorf("whisper calls = %v, want just the full en", ai.calls)
	}
	assertSidecar(t, videos[1], "en")
	bs, err := s.ledger.BackoffsFor(ctx, "m:1")
	if err != nil {
		t.Fatal(err)
	}
	if !bs.BlockedV("m:1", "en", VariantForced, videos[1], time.Now()) || bs.Blocked("m:1", "en", videos[1], time.Now()) {
		t.Errorf("backoff = %+v, want only the forced en parked", bs)
	}
}

// The SDH preference: prefer takes the SDH track (and wins an upload tie), avoid never
// uses one.
func TestSDHPreference(t *testing.T) {
	subs := []SubTrack{
		{Index: 0, Lang: "eng", Text: true, SDH: true, Title: "English SDH"},
		{Index: 1, Lang: "eng", Text: true, Title: "English"},
	}
	for sdh, want := range map[string]int{SDHAny: 1, SDHPrefer: 0, SDHAvoid: 1} {
		if got, ok := pickFullTrackPref(subs, "en", sdh); !ok || got.Index != want {
			t.Errorf("%s picked %+v, want stream %d", sdh, got, want)
		}
	}
	if _, ok := pickFullTrackPref(subs[:1], "en", SDHAvoid); ok {
		t.Error("avoid picked the only (SDH) track")
	}
	if got := bestSourcePref(subs[:1], "en", SDHAvoid, true); got != "download" {
		t.Errorf("avoid with only an SDH track: source %q, want download", got)
	}
	cands := []SubtitleResult{{FileID: "plain", Release: "x"}, {FileID: "hi", Release: "x", HearingImpaired: true}}
	if r := rankCandidates(cands, MatchContext{SDH: SDHPrefer}, nil); r[0].FileID != "hi" {
		t.Errorf("prefer ranked %s first", r[0].FileID)
	}
	if r := rankCandidates(cands, MatchContext{}, nil); r[0].FileID != "plain" {
		t.Errorf("any ranked %s first", r[0].FileID)
	}
	r := rankCandidates(cands, MatchContext{SDH: SDHAvoid}, nil)
	for _, c := range r {
		if c.FileID == "hi" && c.usable() {
			t.Error("avoid left the hearing-impaired upload usable")
		}
	}
}

// A profile change recounts and retries the parked languages; editing a profile no title
// uses recounts nothing; an override recounts just its title, and the counts stay current.
func TestProfileChangesRecount(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en")
	if err := f.s.settings.SetBool(ctx, keyForcedRepairDone, true); err != nil {
		t.Fatal(err) // the one-shot repair would start after the first pass
	}
	p1 := video(t, "One.mkv", "One.en.srt")
	f.movie(t, 1, p1)
	passes := 0
	var pmu sync.Mutex
	done := make(chan struct{}, 8)
	f.s.passFn = func(ctx context.Context) ([]FileSubs, []SeriesGroup, error) {
		pmu.Lock()
		passes++
		pmu.Unlock()
		defer func() { done <- struct{}{} }()
		list, err := f.s.Library(ctx, "movies")
		return list, nil, err
	}
	wait := func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("no pass ran")
		}
		deadline := time.Now().Add(5 * time.Second)
		for f.s.Coverage().Scanning && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}
	count := func() int { pmu.Lock(); defer pmu.Unlock(); return passes }
	f.s.Rescan(ctx)
	wait()
	if c := f.s.Coverage(); c.Movies.Covered != 1 || c.Stale {
		t.Fatalf("coverage = %+v, want one covered movie", c)
	}
	if err := f.s.ledger.SaveBackoff(ctx, Backoff{MediaKey: "m:1", Lang: "fr", VideoPath: p1, PrimaryReason: ResultImpossible, ClearOn: []string{EventLangs}}); err != nil {
		t.Fatal(err)
	}

	// An unused profile: no recount.
	kids, err := f.s.CreateProfile(ctx, "Kids", items("en"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateProfile(ctx, kids.ID, nil, items("de")); err != nil {
		t.Fatal(err)
	}
	if count() != 1 || f.s.Coverage().Stale {
		t.Errorf("editing an unused profile recounted (%d passes)", count())
	}

	// The override: just movie 1 is recomputed, and the counts are current.
	if err := f.s.SetOverride(ctx, OverrideMovie, 1, kids.ID); err != nil {
		t.Fatal(err)
	}
	if c := f.s.Coverage(); c.Movies.Missing != 1 || c.Stale || count() != 1 {
		t.Errorf("after the override: coverage %+v, %d passes; want movie 1 missing de, current, no pass", c, count())
	}
	if bs, _ := f.s.ledger.BackoffsFor(ctx, "m:1"); len(bs) != 0 {
		t.Errorf("langs backoff not cleared: %+v", bs)
	}

	// Editing the default: a full recount.
	f.setDefault(t, items("en/both"))
	wait()
	if count() != 2 || f.s.Coverage().Stale {
		t.Errorf("after editing the default: %d passes, stale %v", count(), f.s.Coverage().Stale)
	}
}

// Convert's "Same as Subtitles" keeps every language any profile wants, the default's first.
func TestKeepLanguagesUnion(t *testing.T) {
	ctx := context.Background()
	f := newProfileFixture(t, "en,fr")
	if _, err := f.s.CreateProfile(ctx, "Anime", items("ja", "en/forced")); err != nil {
		t.Fatal(err)
	}
	if got := f.s.Languages(ctx); !reflect.DeepEqual(got, []string{"en", "fr", "ja"}) {
		t.Errorf("Languages = %v, want [en fr ja]", got)
	}
	if got := f.s.languages(ctx); !reflect.DeepEqual(got, []string{"en", "fr"}) {
		t.Errorf("settings languages = %v, want the default's [en fr]", got)
	}
}
