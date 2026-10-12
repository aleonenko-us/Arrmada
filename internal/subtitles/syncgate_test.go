package subtitles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJudgeSync(t *testing.T) {
	for _, tc := range []struct {
		name        string
		res         SyncResult
		judged      bool
		band        bool
		keep, shift bool
		block       bool
	}{
		{"in sync", SyncResult{OffsetMs: 120, Scale: 1, Confidence: 88}, true, false, true, false, false},
		{"late, confident", SyncResult{OffsetMs: 3000, Scale: 1, Confidence: 85}, true, false, true, true, false},
		{"frame rate, confident", SyncResult{OffsetMs: 0, Scale: 1.0427, Confidence: 75}, true, false, true, true, false},
		{"weak match, confident", SyncResult{OffsetMs: -900, Scale: 1, Confidence: 72}, true, true, true, true, false},
		{"unrelated", SyncResult{OffsetMs: 40000, Scale: 1, Confidence: 18}, true, false, false, false, true},
		{"unrelated weak match", SyncResult{Confidence: 30}, true, true, false, false, true},
		{"uncertain", SyncResult{OffsetMs: 2000, Scale: 1, Confidence: 60}, true, false, true, false, false},
		{"uncertain weak match", SyncResult{Confidence: 60}, true, true, false, false, false},
		{"unjudged", SyncResult{Scale: 1}, false, false, true, false, false},
		{"unjudged weak match", SyncResult{Scale: 1}, false, true, false, false, false},
	} {
		v := judgeSync(tc.res, tc.judged, tc.band)
		if v.keep != tc.keep || v.shift != tc.shift || v.block != tc.block {
			t.Errorf("%s: %+v", tc.name, v)
		}
		if !v.keep && v.detail == "" {
			t.Errorf("%s: refused without saying why", tc.name)
		}
	}
}

// syncProvider serves a different SRT per candidate file id.
type syncProvider struct {
	mu        sync.Mutex
	results   map[string][]SubtitleResult
	files     map[string]string
	downloads []string
}

func (p *syncProvider) Available() bool   { return true }
func (p *syncProvider) CanDownload() bool { return true }
func (p *syncProvider) Search(_ context.Context, r SearchRequest) ([]SubtitleResult, error) {
	return p.results[r.Language], nil
}
func (p *syncProvider) Download(_ context.Context, id string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloads = append(p.downloads, id)
	return []byte(p.files[id]), nil
}

// syncFixture is a two-language movie whose audio is tone bursts at known times; the
// extraction seam writes that WAV and counts how often it was asked to.
func syncFixture(t *testing.T, prov Provider, ai aiRunner) (*Service, string, []cue, *int, *[]string) {
	t.Helper()
	mi := englishAudio()
	mi.FPS = 23.976
	s, video := ladderFixture(t, "en,es", mi, prov, ai)
	s.resolve = func(context.Context, *Job) (fileRef, bool) {
		return fileRef{Path: video, Title: "Movie", Year: 2020, MediaKey: "m:1", SourceRelease: bluray2160}, true
	}
	s.syncGate = s.gateSync
	speech := synthSpeech(8, 4*time.Minute)
	var mu sync.Mutex
	calls := 0
	var wavs []string
	s.extractAudio = func(_ context.Context, _ string, stream int, dst string) error {
		mu.Lock()
		calls++
		wavs = append(wavs, dst)
		mu.Unlock()
		if stream != 0 {
			t.Errorf("checked against audio stream %d, want the English track 0", stream)
		}
		writeToneWAV(t, dst, 4*time.Minute, speech)
		return nil
	}
	return s, video, speech, &calls, &wavs
}

func candidate(id string) SubtitleResult {
	return SubtitleResult{FileID: id, Release: bluray2160, FPS: 23.976} // scores 60: a name match, no hash
}

// An unrelated download is refused and blocklisted, the next candidate (3 s late) is
// shifted into sync and saved with its Health, a subtitle already in sync is saved as it
// came, and the audio is read once for both languages and deleted after the job.
func TestSyncGateRejectsShiftsAndShares(t *testing.T) {
	speech := synthSpeech(8, 4*time.Minute) // what syncFixture's audio says
	unrelated := formatSRT(subtitleFor(synthSpeech(909, 4*time.Minute), 3))
	late := formatSRT(shiftCues(subtitleFor(speech, 5), 3000, 1))
	inSync := formatSRT(subtitleFor(speech, 6))
	prov := &syncProvider{
		results: map[string][]SubtitleResult{"en": {candidate("bad"), candidate("late")}, "es": {candidate("es")}},
		files:   map[string]string{"bad": unrelated, "late": late, "es": inSync},
	}
	ai := &fakeAI{avail: true, transcribe: true, translate: true}
	s, video, _, calls, wavs := syncFixture(t, prov, ai)
	job := runJob(s)
	if job.State != StateDone || len(ai.calls) != 0 {
		t.Fatalf("state %q note %q ai %v", job.State, job.Note, ai.calls)
	}
	if got := strings.Join(prov.downloads, ","); got != "bad,late,es" {
		t.Errorf("downloads %s", got)
	}
	if *calls != 1 {
		t.Errorf("audio extracted %d times, want once for both languages", *calls)
	}
	for _, w := range *wavs {
		if _, err := os.Stat(w); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("WAV %s left behind after the job (%v)", w, err)
		}
	}
	ctx := context.Background()
	blocked, err := s.ledger.BlockedFor(ctx, "m:1")
	if err != nil || len(blocked) != 1 || blocked[0].FileID != "bad" || blocked[0].Reason != BlockOutOfSync {
		t.Errorf("blocklist %+v (%v), want the unrelated file as out_of_sync", blocked, err)
	}

	// The late one was moved: its first cue now sits where the speech starts.
	raw, err := os.ReadFile(sidecarPath(video, "en"))
	if err != nil {
		t.Fatal(err)
	}
	want, got := parseSRT(late)[0].start-3*time.Second, parseSRT(string(raw))[0].start
	if d := got - want; d < -250*time.Millisecond || d > 250*time.Millisecond {
		t.Errorf("first cue at %v, want ≈ %v", got, want)
	}
	en, ok, err := s.ledger.LiveFile(ctx, sidecarPath(video, "en"))
	if err != nil || !ok || en.SyncConfidence == nil || *en.SyncConfidence < syncKeep || en.SyncShiftMs == nil ||
		*en.SyncShiftMs > -2700 || *en.SyncShiftMs < -3300 || en.SyncCheckedAt == 0 {
		t.Fatalf("en row %+v ok=%v err=%v", en, ok, err)
	}
	if h := healthOf(en); h.State != HealthOK || !strings.HasPrefix(h.Note, "shifted −") {
		t.Errorf("en health %+v", h)
	}
	// In sync already: saved byte for byte, checked, not shifted.
	if raw, _ := os.ReadFile(sidecarPath(video, "es")); string(raw) != inSync {
		t.Error("an in-sync subtitle was rewritten")
	}
	es, _, _ := s.ledger.LiveFile(ctx, sidecarPath(video, "es"))
	if es.SyncConfidence == nil || es.SyncShiftMs != nil {
		t.Errorf("es row %+v", es)
	}
	if h := healthOf(es); h.State != HealthOK || !strings.HasPrefix(h.Note, "in sync") {
		t.Errorf("es health %+v", h)
	}
}

// When every candidate is out of sync the language goes to the AI, and the attempt says
// why the download was refused.
func TestSyncGateAllRefusedFallsToAI(t *testing.T) {
	prov := &syncProvider{
		results: map[string][]SubtitleResult{"en": {candidate("x"), candidate("y")}},
		files: map[string]string{
			"x": formatSRT(subtitleFor(synthSpeech(31, 4*time.Minute), 1)),
			"y": formatSRT(subtitleFor(synthSpeech(77, 4*time.Minute), 2)),
		},
	}
	ai := &fakeAI{avail: true, transcribe: true, translate: true}
	s, video, _, calls, _ := syncFixture(t, prov, ai)
	s.settings.Set(context.Background(), keyLanguages, "en")
	job := runJob(s)
	if job.State != StateDone || len(ai.calls) != 1 || *calls != 1 {
		t.Fatalf("state %q note %q ai %v extractions %d", job.State, job.Note, ai.calls, *calls)
	}
	if !strings.Contains(job.Note, "out of sync") || !strings.Contains(job.Note, "AI-generated") {
		t.Errorf("note %q", job.Note)
	}
	got := map[Result]bool{}
	for _, o := range job.Outcomes {
		got[o.Result] = true
	}
	if !got[ResultOutOfSync] {
		t.Errorf("outcomes %+v, want out_of_sync for the download", job.Outcomes)
	}
	assertSidecar(t, video, "en")
	if blocked, _ := s.ledger.Blocklisted(context.Background(), providerOpenSubtitles, "m:1"); !blocked["x"] || !blocked["y"] {
		t.Errorf("blocklist %v", blocked)
	}
}

// A weak name match isn't even downloaded when the audio can't be read to check it; a
// good name match is saved unchecked.
func TestSyncGateUnreadableAudio(t *testing.T) {
	weak := SubtitleResult{FileID: "weak", Release: "Movie.2020.1080p.BluRay.x264-OTHER", FPS: 23.976} // 45
	prov := &syncProvider{results: map[string][]SubtitleResult{"en": {weak}}, files: map[string]string{"weak": fakeSRT}}
	ai := &fakeAI{avail: true, transcribe: true}
	s, video, _, _, _ := syncFixture(t, prov, ai)
	s.settings.Set(context.Background(), keyLanguages, "en")
	s.extractAudio = func(context.Context, string, int, string) error { return errors.New("no audio stream") }
	job := runJob(s)
	if len(prov.downloads) != 0 || len(ai.calls) != 1 || !strings.Contains(job.Note, "couldn't be checked") {
		t.Errorf("downloads %v ai %v note %q", prov.downloads, ai.calls, job.Note)
	}

	prov.results["en"] = []SubtitleResult{candidate("good")}
	prov.files["good"] = fakeSRT
	_ = os.Remove(sidecarPath(video, "en"))
	ai.calls = nil
	job = runJob(s)
	if job.State != StateDone || len(ai.calls) != 0 || strings.Join(prov.downloads, ",") != "good" {
		t.Fatalf("state %q note %q downloads %v ai %v", job.State, job.Note, prov.downloads, ai.calls)
	}
	f, _, _ := s.ledger.LiveFile(context.Background(), sidecarPath(video, "en"))
	if f.SyncConfidence != nil || healthOf(f).State != HealthUnchecked {
		t.Errorf("unchecked save recorded a check: %+v", f)
	}
}

func TestHealthOf(t *testing.T) {
	ip := func(n int) *int { return &n }
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local).Unix()
	for _, tc := range []struct {
		f     SubtitleFile
		state string
		note  string
	}{
		{SubtitleFile{Source: SourceEmbedded}, HealthUnchecked, "not checked · from the file's own track"},
		{SubtitleFile{Source: SourceOpenSubtitles, HashMatch: true}, HealthUnchecked, "not checked · hash-matched to this file"},
		{SubtitleFile{SyncConfidence: ip(84), SyncOffsetMs: ip(3010), SyncShiftMs: ip(-3010), SyncCheckedAt: at}, HealthOK, "shifted −3.0 s · checked 2026-10-12"},
		{SubtitleFile{SyncConfidence: ip(91), SyncOffsetMs: ip(40), SyncCheckedAt: at}, HealthOK, "in sync · checked 2026-10-12"},
		{SubtitleFile{SyncConfidence: ip(62), SyncOffsetMs: ip(-1500), SyncCheckedAt: at}, HealthUncertain, "runs 1.5 s early · uncertain · checked 2026-10-12"},
		{SubtitleFile{SyncConfidence: ip(20), SyncOffsetMs: ip(0), SyncCheckedAt: at}, HealthBad, "doesn't match the speech · checked 2026-10-12"},
	} {
		h := healthOf(tc.f)
		if h.State != tc.state || h.Note != tc.note {
			t.Errorf("%+v → %q %q, want %q %q", tc.f, h.State, h.Note, tc.state, tc.note)
		}
	}
}

// A file's Health is its worst present language's; rows for another video (an upgraded
// movie) and missing languages don't count.
func TestApplyHealth(t *testing.T) {
	ip := func(n int) *int { return &n }
	fs := FileSubs{Path: "/m/Movie.mkv", Languages: []LangStatus{{Lang: "en", Have: true}, {Lang: "es", Have: true}, {Lang: "fr"}}}
	applyHealth(&fs, []SubtitleFile{
		{VideoPath: "/m/Movie.mkv", Lang: "en", SyncConfidence: ip(88), SyncOffsetMs: ip(0)},
		{VideoPath: "/m/Old.mkv", Lang: "es", SyncConfidence: ip(90), SyncOffsetMs: ip(0)},
		{VideoPath: "/m/Movie.mkv", Lang: "fr", SyncConfidence: ip(10), SyncOffsetMs: ip(0)},
	})
	if h := fs.Languages[0].Health; h == nil || h.State != HealthOK || h.Score != 88 {
		t.Errorf("en %+v", h)
	}
	if h := fs.Languages[1].Health; h == nil || h.State != HealthUnchecked {
		t.Errorf("es %+v", h)
	}
	if fs.Languages[2].Health != nil {
		t.Error("a missing language got Health")
	}
	if fs.Health == nil || fs.Health.State != HealthUnchecked {
		t.Errorf("file health %+v, want the unchecked language's", fs.Health)
	}
}

// The on-demand check finds the sidecar from the catalog (any spelling of the language),
// measures it without changing it, and records Health on an unowned file.
func TestCheckSyncOnDemand(t *testing.T) {
	s, video, speech, _, _ := syncFixture(t, &fakeProvider{}, &fakeAI{})
	ctx := context.Background()
	if _, err := s.CheckSync(ctx, MediaRef{Kind: "movie", MovieID: 1}, "en", ""); !errors.Is(err, ErrNoSidecar) {
		t.Errorf("no sidecar: %v", err)
	}
	s.resolve = func(context.Context, *Job) (fileRef, bool) { return fileRef{}, false }
	if _, err := s.CheckSync(ctx, MediaRef{Kind: "movie", MovieID: 9}, "en", ""); !errors.Is(err, ErrNoVideo) {
		t.Errorf("unknown movie: %v", err)
	}
	if _, err := s.SyncTarget(ctx, MediaRef{Kind: "../etc"}, "en", ""); !errors.Is(err, ErrNoVideo) {
		t.Errorf("bad kind: %v", err)
	}
	s.resolve = func(context.Context, *Job) (fileRef, bool) {
		return fileRef{Path: video, Title: "Movie", MediaKey: "m:1"}, true
	}
	side := strings.TrimSuffix(video, filepath.Ext(video)) + ".eng.srt"
	late := formatSRT(shiftCues(subtitleFor(speech, 5), 2000, 1))
	if err := os.WriteFile(side, []byte(late), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := s.CheckSync(ctx, MediaRef{Kind: "movie", MovieID: 1}, "eng", "full")
	if err != nil || !rep.Judged || rep.Health.State != HealthOK || rep.Path != filepath.Base(side) {
		t.Fatalf("report %+v err %v", rep, err)
	}
	if raw, _ := os.ReadFile(side); string(raw) != late {
		t.Error("the check changed the file")
	}
	f, ok, _ := s.ledger.LiveFile(ctx, side)
	if !ok || f.Source != SourceUnknown || f.SyncOffsetMs == nil || *f.SyncOffsetMs < 1700 || *f.SyncOffsetMs > 2300 {
		t.Errorf("row %+v ok=%v", f, ok)
	}
	if h := healthOf(f); !strings.HasPrefix(h.Note, "runs ") || !strings.Contains(h.Note, " s late · checked ") {
		t.Errorf("health note %q", h.Note)
	}
	// An .ass sidecar can't be measured.
	_ = os.Remove(side)
	_ = os.WriteFile(strings.TrimSuffix(video, filepath.Ext(video))+".es.ass", []byte("[Script Info]"), 0o644)
	if _, err := s.CheckSync(ctx, MediaRef{Kind: "movie", MovieID: 1}, "es", ""); !errors.Is(err, ErrNotSRT) {
		t.Errorf("ass: %v", err)
	}
}

func TestLedgerRecordSync(t *testing.T) {
	s, video, _, _, _ := syncFixture(t, &fakeProvider{}, &fakeAI{})
	ctx := context.Background()
	p := sidecarPath(video, "en")
	if ok, err := s.ledger.RecordSync(ctx, p, SyncCheck{OffsetMs: 10, Confidence: 80}); ok || err != nil {
		t.Fatalf("no row: ok=%v err=%v", ok, err)
	}
	if _, err := s.ledger.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", VideoPath: video, Path: p, Lang: "en", Source: SourceOpenSubtitles}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ledger.RecordSync(ctx, p, SyncCheck{OffsetMs: 2500, Confidence: 77, ShiftMs: -2500, Scale: 1.0427}); !ok || err != nil {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}
	rows, err := s.ledger.LiveFiles(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("live %v %v", rows, err)
	}
	f := rows[0]
	if *f.SyncConfidence != 77 || *f.SyncShiftMs != -2500 || f.SyncScale == nil || *f.SyncScale != 1.0427 || f.SyncCheckedAt == 0 {
		t.Errorf("row %+v", f)
	}
	// A later plain check clears the old correction.
	if ok, _ := s.ledger.SetSync(ctx, p, 40, 90); !ok {
		t.Fatal("SetSync found no row")
	}
	f, _, _ = s.ledger.LiveFile(ctx, p)
	if f.SyncShiftMs != nil || f.SyncScale != nil || *f.SyncOffsetMs != 40 {
		t.Errorf("after SetSync %+v", f)
	}
}
