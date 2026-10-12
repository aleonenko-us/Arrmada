package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/movies"
)

func TestBackoffSchedule(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Hour)
	o := func(r Rung, res Result) Outcome {
		return Outcome{Lang: "en", Rung: r, Result: res, Detail: string(res)}
	}
	cases := []struct {
		name    string
		prev    *Backoff
		outs    []Outcome
		park    bool
		primary Result
		count   int
		next    time.Duration // 0 = NULL
		clearOn []string
	}{
		{"made it", nil, []Outcome{o(RungOpenSubtitles, ResultNoMatch), o(RungAI, ResultOK)}, false, "", 0, 0, nil},
		{"stopped", nil, []Outcome{o(RungAI, ResultCancelled)}, false, "", 0, 0, nil},
		{"no match, can't AI", nil, []Outcome{o(RungOpenSubtitles, ResultNoMatch), o(RungAI, ResultImpossible)},
			true, ResultNoMatch, 1, 7 * day, []string{EventLangs}},
		{"no match again", &Backoff{VideoPath: "/v.mkv", PrimaryReason: ResultNoMatch, Count: 1},
			[]Outcome{o(RungOpenSubtitles, ResultNoMatch), o(RungAI, ResultImpossible)}, true, ResultNoMatch, 2, 30 * day, []string{EventLangs}},
		{"no match a third time", &Backoff{VideoPath: "/v.mkv", PrimaryReason: ResultNoMatch, Count: 5},
			[]Outcome{o(RungOpenSubtitles, ResultNoMatch)}, true, ResultNoMatch, 6, 90 * day, nil},
		{"new file starts over", &Backoff{VideoPath: "/old.mkv", PrimaryReason: ResultNoMatch, Count: 2},
			[]Outcome{o(RungOpenSubtitles, ResultNoMatch)}, true, ResultNoMatch, 1, 7 * day, nil},
		{"quota waits for the reset", nil, []Outcome{o(RungOpenSubtitles, ResultQuota), o(RungAI, ResultModelMissing)},
			true, ResultModelMissing, 1, 5 * time.Hour, []string{EventModel}},
		{"rate limited", nil, []Outcome{o(RungOpenSubtitles, ResultRateLimited), o(RungAI, ResultImpossible)},
			true, ResultRateLimited, 1, 10 * time.Minute, []string{EventLangs}},
		{"provider error", &Backoff{VideoPath: "/v.mkv", PrimaryReason: ResultProviderError, Count: 1},
			[]Outcome{o(RungOpenSubtitles, ResultProviderError), o(RungAI, ResultImpossible)}, true, ResultProviderError, 2, day, []string{EventLangs}},
		{"login rejected, no model", nil, []Outcome{o(RungOpenSubtitles, ResultAuthFailed), o(RungAI, ResultModelMissing)},
			true, ResultAuthFailed, 1, 0, []string{EventCreds, EventModel}},
		{"AI failed", nil, []Outcome{o(RungAI, ResultAIFailed)}, true, ResultAIFailed, 1, day, nil},
		{"AI failed thrice: by hand only", &Backoff{VideoPath: "/v.mkv", PrimaryReason: ResultAIFailed, Count: 2},
			[]Outcome{o(RungAI, ResultAIFailed)}, true, ResultAIFailed, 3, 0, nil},
		{"empty extract then no match", nil, []Outcome{o(RungExtract, ResultEmpty), o(RungOpenSubtitles, ResultNoMatch), o(RungAI, ResultImpossible)},
			true, ResultNoMatch, 1, 7 * day, []string{EventLangs}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, park := planBackoff(c.prev, "m:1", "EN", "/v.mkv", c.outs, now, reset)
			if park != c.park {
				t.Fatalf("park = %v, want %v", park, c.park)
			}
			if !park {
				return
			}
			if b.PrimaryReason != c.primary || b.Count != c.count || b.Lang != "en" {
				t.Errorf("primary=%s count=%d lang=%s, want %s %d en", b.PrimaryReason, b.Count, b.Lang, c.primary, c.count)
			}
			var want int64
			if c.next > 0 {
				want = now.Add(c.next).Unix()
			}
			if b.NextRetryAt != want {
				t.Errorf("next_retry_at = %v, want %v", time.Unix(b.NextRetryAt, 0).UTC(), time.Unix(want, 0).UTC())
			}
			if !reflect.DeepEqual(b.ClearOn, c.clearOn) {
				t.Errorf("clear_on = %v, want %v", b.ClearOn, c.clearOn)
			}
		})
	}
}

// sweepFixture is a Service over a real movies module with two movies whose videos (and
// no sidecars) are in temp dirs.
func sweepFixture(t *testing.T) (*Service, []string) {
	t.Helper()
	s, _ := ladderFixture(t, "en", englishAudio(), nil, &fakeAI{})
	s.movies = movies.NewService(s.ledger.db, nil, nil, t.TempDir(), "", nil, s.log)
	var paths []string
	for i, title := range []string{"Heat", "Ran"} {
		p := filepath.Join(t.TempDir(), title+".mkv")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ledger.db.Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (?, ?, ?, 1, 1, ?)`,
			i+1, 100+i, title, p); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return s, paths
}

func queuedMovies(s *Service) []int64 {
	var ids []int64
	for _, j := range s.Jobs() {
		if j.State == StateQueued {
			ids = append(ids, j.MovieID)
		}
	}
	return ids
}

// A language backing off for that exact file isn't queued by the sweep; one whose wait
// is over, or that was parked for a different (since upgraded) file, is.
func TestSweepHonoursBackoff(t *testing.T) {
	s, paths := sweepFixture(t)
	ctx := context.Background()
	future := time.Now().Add(6 * day).Unix()
	if err := s.ledger.SaveBackoff(ctx, Backoff{MediaKey: "m:1", Lang: "en", VideoPath: paths[0], PrimaryReason: ResultNoMatch,
		Reasons: map[Rung]Result{RungOpenSubtitles: ResultNoMatch}, Count: 1, NextRetryAt: future}); err != nil {
		t.Fatal(err)
	}
	if err := s.ledger.SaveBackoff(ctx, Backoff{MediaKey: "m:2", Lang: "en", VideoPath: "/old/Ran.avi", PrimaryReason: ResultNoMatch,
		Reasons: map[Rung]Result{RungOpenSubtitles: ResultNoMatch}, Count: 1, NextRetryAt: future}); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepMissing(ctx, "movies")
	if err != nil || n != 1 {
		t.Fatalf("queued %d (%v), want 1", n, err)
	}
	if got := queuedMovies(s); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("queued movies = %v, want only Ran (its backoff was for another file)", got)
	}
}

// TestBackoffResetsOnFileChange: a row only holds for the video it was written for.
func TestBackoffResetsOnFileChange(t *testing.T) {
	b := Backoff{VideoPath: "/m/Heat.WEB.mkv", NextRetryAt: time.Now().Add(day).Unix()}
	if !b.Blocked("/m/Heat.WEB.mkv", time.Now()) || b.Blocked("/m/Heat.BluRay.mkv", time.Now()) {
		t.Error("a backoff must hold for its own file and not for an upgrade")
	}
	if b.Blocked("/m/Heat.WEB.mkv", time.Now().Add(2*day)) {
		t.Error("a backoff past its retry time still blocks")
	}
	parked := Backoff{VideoPath: "/m/Heat.WEB.mkv"} // event-only
	if !parked.Blocked("/m/Heat.WEB.mkv", time.Now().Add(365*day)) {
		t.Error("an event-only backoff stopped blocking on its own")
	}
}

func TestCredentialChangeClearsAuthBackoff(t *testing.T) {
	s, _ := sweepFixture(t)
	ctx := context.Background()
	for _, b := range []Backoff{
		{MediaKey: "m:1", Lang: "en", VideoPath: "/a", PrimaryReason: ResultAuthFailed, ClearOn: []string{EventCreds, EventModel}},
		{MediaKey: "m:2", Lang: "en", VideoPath: "/b", PrimaryReason: ResultModelMissing, ClearOn: []string{EventModel}},
		{MediaKey: "m:3", Lang: "en", VideoPath: "/c", PrimaryReason: ResultNoMatch, NextRetryAt: time.Now().Add(day).Unix()},
	} {
		b.Count = 1
		if err := s.ledger.SaveBackoff(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	s.CredentialsChanged(ctx)
	left, _ := s.ledger.LoadBackoffs(ctx)
	if _, ok := left[BackoffKey("m:1", "en", VariantFull)]; ok || len(left) != 2 {
		t.Errorf("after a credential change: %v, want only the login-parked row gone", keysOf(left))
	}
	// TestModelDownloadClearsModelBackoff: a downloaded model clears what waited on one.
	s.ClearBackoff(ctx, EventModel)
	left, _ = s.ledger.LoadBackoffs(ctx)
	if _, ok := left[BackoffKey("m:3", "en", VariantFull)]; !ok || len(left) != 1 {
		t.Errorf("after a model download: %v, want only the timed row", keysOf(left))
	}
}

func keysOf(bs Backoffs) []string {
	var out []string
	for k := range bs {
		out = append(out, k)
	}
	return out
}

// A run parks what it couldn't make, a sweep job skips a parked language without a single
// search, and a button press (manual priority) tries anyway — and clears the row once it
// makes the subtitle.
func TestManualQueueIgnoresBackoff(t *testing.T) {
	prov := &fakeProvider{canDownload: true}
	ai := &fakeAI{avail: true, transcribe: true}
	fre := &mediaInfo{AudioLangs: []string{"fre"}, Audio: []AudioTrack{{Index: 0, Lang: "fre"}}}
	s, video := ladderFixture(t, "es", fre, prov, ai)
	ctx := context.Background()

	// First run: no match, and whisper can't make Spanish from French audio → parked 7 days.
	job := runJob(s)
	if job.State != StateSkipped {
		t.Fatalf("first run: state %q note %q", job.State, job.Note)
	}
	bs, _ := s.ledger.BackoffsFor(ctx, "m:1")
	b, ok := bs[BackoffKey("m:1", "es", VariantFull)]
	if !ok || b.PrimaryReason != ResultNoMatch || b.VideoPath != video || b.NextRetryAt < time.Now().Add(6*day).Unix() {
		t.Fatalf("backoff after the first run = %+v (ok=%v)", b, ok)
	}

	// A sweep job queued before that run: skipped, no search, and the note says when.
	sweep := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Priority: PrioSweep})
	s.pop()
	s.process(ctx, sweep)
	if prov.searches != 1 || sweep.State != StateSkipped || !strings.Contains(sweep.Note, "es: no OpenSubtitles match — next try") {
		t.Errorf("sweep job: searches=%d state=%q note=%q", prov.searches, sweep.State, sweep.Note)
	}

	// Ensure (manual) runs it anyway; a match now clears the row.
	prov.results = map[string][]SubtitleResult{"es": {{FileID: "5", HashMatch: true}}}
	manual := runJob(s)
	if prov.searches != 2 || manual.State != StateDone {
		t.Errorf("manual job: searches=%d state=%q note=%q", prov.searches, manual.State, manual.Note)
	}
	if bs, _ := s.ledger.BackoffsFor(ctx, "m:1"); len(bs) != 0 {
		t.Errorf("backoff left after the subtitle was made: %+v", bs)
	}
}

// The Library chip carries the backoff's reason and next try for a missing language.
func TestApplyBackoffToLibrary(t *testing.T) {
	next := time.Now().Add(6 * day).Unix()
	bs := Backoffs{
		BackoffKey("e:4:1:2", "en", VariantFull): {MediaKey: "e:4:1:2", Lang: "en", VideoPath: "/tv/e.mkv", PrimaryReason: ResultNoMatch, NextRetryAt: next},
		BackoffKey("e:4:1:2", "es", VariantFull): {MediaKey: "e:4:1:2", Lang: "es", VideoPath: "/tv/e.mkv", PrimaryReason: ResultAuthFailed, ClearOn: []string{EventCreds}},
	}
	fs := FileSubs{Kind: "episode", SeriesID: 4, Season: 1, Episode: 2, Path: "/tv/e.mkv",
		Languages: []LangStatus{{Lang: "en"}, {Lang: "es"}, {Lang: "fr"}, {Lang: "de", Have: true}}}
	applyBackoff(&fs, bs, time.Now())
	en, es, fr := fs.Languages[0], fs.Languages[1], fs.Languages[2]
	if en.Reason != "no_match" || en.NextRetryAt != next || es.Reason != "auth_failed" || es.RetryOn != "creds" || fr.Reason != "" {
		t.Errorf("languages = %+v", fs.Languages)
	}
}
