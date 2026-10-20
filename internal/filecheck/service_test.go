package filecheck

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/store"
)

// fakeProber answers by path, counting reads.
type fakeProber struct {
	mu    sync.Mutex
	by    map[string]Probe
	reads int
}

func (f *fakeProber) Probe(_ context.Context, path string) Probe {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.by[path]
}

type fakeLibrary struct{ files []File }

func (l *fakeLibrary) Files(context.Context) ([]File, error) { return l.files, nil }
func (l *fakeLibrary) MovieFiles(_ context.Context, id int64) ([]File, error) {
	var out []File
	for _, f := range l.files {
		if f.Kind == KindMovie && f.OwnerID == id {
			out = append(out, f)
		}
	}
	return out, nil
}
func (l *fakeLibrary) SeriesFiles(_ context.Context, id int64) ([]File, error) {
	var out []File
	for _, f := range l.files {
		if f.Kind == KindEpisode && f.OwnerID == id {
			out = append(out, f)
		}
	}
	return out, nil
}

type fakeSettings struct{ m map[string]bool }

func (s *fakeSettings) GetBool(_ context.Context, k string, def bool) bool {
	if v, ok := s.m[k]; ok {
		return v
	}
	return def
}
func (s *fakeSettings) SetBool(_ context.Context, k string, v bool) error { s.m[k] = v; return nil }

type harness struct {
	svc      *Service
	probe    *fakeProber
	lib      *fakeLibrary
	settings *fakeSettings
	replaced []Stored
	fail     error
	clock    time.Time
	dir      string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := &harness{probe: &fakeProber{by: map[string]Probe{}}, lib: &fakeLibrary{}, settings: &fakeSettings{m: map[string]bool{}},
		clock: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), dir: t.TempDir()}
	h.svc = New(st.DB(), h.lib, h.probe, h.settings, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.svc.now = func() time.Time { return h.clock }
	h.svc.SetReplacer(func(_ context.Context, f Stored) error {
		if h.fail != nil {
			return h.fail
		}
		h.replaced = append(h.replaced, f)
		return nil
	})
	return h
}

// file writes a file on disk and adds it to the library with the given probe.
func (h *harness) file(t *testing.T, f File, p Probe) File {
	t.Helper()
	f.Path = filepath.Join(h.dir, f.Title+".mkv")
	if err := os.WriteFile(f.Path, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.probe.by[f.Path] = p
	h.lib.files = append(h.lib.files, f)
	return f
}

var good = Probe{AudioLangs: []string{"eng"}, DurationSec: 7000, Loudness: []float64{-3, -2, -4}, Windows: 3}

func TestPassRecordsAndOnlyRereadsChangedFiles(t *testing.T) {
	h := newHarness(t)
	h.settings.m[KeyAutoReplace] = false
	ctx := context.Background()
	mute := h.file(t, File{Kind: KindMovie, MediaID: 1, OwnerID: 1, Title: "The Matrix (1999)", Release: "The.Matrix.1999.1080p.BluRay.x264-GRP"},
		Probe{DurationSec: 8000})
	h.file(t, File{Kind: KindEpisode, MediaID: 10, OwnerID: 5, Season: 1, Episode: 2, Title: "Severance S01E02"}, good)

	res, err := h.svc.Pass(ctx, false)
	if err != nil || res.Files != 2 || res.Checked != 2 {
		t.Fatalf("first pass = %+v, %v", res, err)
	}
	ps, _ := h.svc.Findings(ctx, false)
	if len(ps) != 1 || ps[0].Code != CodeNoAudio || ps[0].Text != "The Matrix (1999): the file has no sound" || ps[0].State != StateFlagged {
		t.Fatalf("findings = %+v", ps)
	}
	// Nothing changed: nothing read again.
	if res, _ := h.svc.Pass(ctx, false); res.Checked != 0 || h.probe.reads != 2 {
		t.Fatalf("second pass read %d (%d reads)", res.Checked, h.probe.reads)
	}
	// Ignored, it stays off the list; the same file checked again keeps it ignored.
	if err := h.svc.Dismiss(ctx, ps[0].ID); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.svc.Pass(ctx, true); res.Checked != 2 {
		t.Fatalf("forced pass read %d", res.Checked)
	}
	if ps, _ := h.svc.Findings(ctx, false); len(ps) != 0 {
		t.Fatalf("an ignored finding came back: %+v", ps)
	}
	// A new file in its place (an upgrade): checked afresh, the old dismissal gone with it.
	if err := os.WriteFile(mute.Path, []byte("a different, longer file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.svc.Pass(ctx, false); res.Checked != 1 {
		t.Fatalf("changed file: read %d", res.Checked)
	}
	if ps, _ := h.svc.Findings(ctx, false); len(ps) != 1 || ps[0].Dismissed {
		t.Fatalf("a changed file's finding: %+v", ps)
	}
	// Gone from the library: forgotten.
	h.lib.files = h.lib.files[1:]
	if _, err := h.svc.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	if ps, _ := h.svc.Findings(ctx, true); len(ps) != 0 {
		t.Fatalf("a removed file's findings stayed: %+v", ps)
	}
}

// The loop guard: one automatic replace a day per title, three in all, then it gives up
// and says so — and a wrong language is never replaced automatically.
func TestAutoReplaceCaps(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.file(t, File{Kind: KindMovie, MediaID: 7, OwnerID: 7, Title: "Resident Evil (2026)", Release: "Resident.Evil.2026.HDCAM.x264-GRP"}, good)
	h.file(t, File{Kind: KindMovie, MediaID: 8, OwnerID: 8, Title: "Heat (1995)"},
		Probe{AudioLangs: []string{"rus"}, DurationSec: 7000})

	if _, err := h.svc.Pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	if len(h.replaced) != 1 || h.replaced[0].MediaID != 7 || h.replaced[0].Code != CodeSuspectedCam {
		t.Fatalf("replaced = %+v, want the cam only", h.replaced)
	}
	ps, _ := h.svc.FindingsFor(ctx, KindMovie, 7)
	if len(ps) != 1 || ps[0].Text != "Resident Evil (2026): probably a cam — blocklisted it and looking for a better copy" {
		t.Fatalf("problem = %+v", ps)
	}
	// Within the day: nothing more.
	h.clock = h.clock.Add(23 * time.Hour)
	h.svc.AutoReplace(ctx)
	if len(h.replaced) != 1 {
		t.Fatalf("replaced again inside a day: %d", len(h.replaced))
	}
	// A day on, twice more: three in all.
	for i := 0; i < 2; i++ {
		h.clock = h.clock.Add(25 * time.Hour)
		h.svc.AutoReplace(ctx)
	}
	if len(h.replaced) != 3 {
		t.Fatalf("replaced %d times, want 3", len(h.replaced))
	}
	// The fourth day: it has given up.
	h.clock = h.clock.Add(48 * time.Hour)
	h.svc.AutoReplace(ctx)
	if len(h.replaced) != 3 {
		t.Fatalf("a fourth automatic replace: %d", len(h.replaced))
	}
	ps, _ = h.svc.FindingsFor(ctx, KindMovie, 7)
	if len(ps) != 1 || ps[0].State != StateGaveUp || !ps[0].Error ||
		ps[0].Text != "Resident Evil (2026): probably a cam — automatic replacement gave up after 3 tries" {
		t.Fatalf("after giving up = %+v", ps)
	}
	// Find a better copy by hand still works, and the item says it's looking again.
	if _, err := h.svc.ReplaceNow(ctx, ps[0].ID); err != nil {
		t.Fatal(err)
	}
	if ps, _ = h.svc.FindingsFor(ctx, KindMovie, 7); ps[0].State != StateReplacing {
		t.Fatalf("after a replace by hand: %+v", ps[0])
	}
	// The wrong-language file was only flagged.
	ps, _ = h.svc.FindingsFor(ctx, KindMovie, 8)
	if len(ps) != 1 || ps[0].Code != CodeWrongLanguage || ps[0].Text != "Heat (1995): audio is in Russian only" {
		t.Fatalf("wrong language = %+v", ps)
	}
}

func TestAutoReplaceOffOnlyFlags(t *testing.T) {
	h := newHarness(t)
	h.settings.m[KeyAutoReplace] = false
	ctx := context.Background()
	h.file(t, File{Kind: KindEpisode, MediaID: 3, OwnerID: 2, Title: "Show S01E01"}, Probe{DurationSec: 3000})
	if err := h.svc.CheckSeries(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if len(h.replaced) != 0 {
		t.Fatalf("replaced with the setting off: %+v", h.replaced)
	}
	// Switching it on acts at once.
	if err := h.svc.SetAutoReplace(ctx, true); err != nil {
		t.Fatal(err)
	}
	if len(h.replaced) != 1 || h.replaced[0].Code != CodeNoAudio {
		t.Fatalf("after switching on: %+v", h.replaced)
	}
	ps, _ := h.svc.FindingsFor(ctx, KindEpisode, 2)
	if len(ps) != 1 || ps[0].Text != "Show S01E01: the file has no sound — looking for a better copy" {
		t.Fatalf("problem = %+v", ps)
	}
}

// A replace that fails isn't counted against the limit.
func TestAutoReplaceFailureNotCounted(t *testing.T) {
	h := newHarness(t)
	h.fail = errors.New("indexers down")
	ctx := context.Background()
	h.file(t, File{Kind: KindMovie, MediaID: 4, OwnerID: 4, Title: "Film (2026)"}, Probe{Err: errors.New("bad file")})
	if err := h.svc.CheckMovie(ctx, 4); err != nil {
		t.Fatal(err)
	}
	h.fail = nil
	h.svc.AutoReplace(ctx)
	if len(h.replaced) != 1 {
		t.Fatalf("replaced = %d, want the retry to go through", len(h.replaced))
	}
}

func TestCapDecision(t *testing.T) {
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name        string
		hist        []replacement
		act, gaveUp bool
	}{
		{"never", nil, true, false},
		{"by hand only", []replacement{{At: now.Add(-time.Hour)}}, true, false},
		{"one an hour ago", []replacement{{At: now.Add(-time.Hour), Auto: true}}, false, false},
		{"one two days ago", []replacement{{At: now.Add(-2 * day), Auto: true}}, true, false},
		{"three", []replacement{{At: now.Add(-9 * day), Auto: true}, {At: now.Add(-8 * day), Auto: true}, {At: now.Add(-7 * day), Auto: true}}, false, true},
	}
	for _, tc := range cases {
		act, gaveUp := capDecision(tc.hist, now)
		if act != tc.act || gaveUp != tc.gaveUp {
			t.Errorf("%s: act %v gaveUp %v, want %v %v", tc.name, act, gaveUp, tc.act, tc.gaveUp)
		}
	}
}
