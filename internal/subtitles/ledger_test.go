package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/store"
)

func ledgerFixture(t *testing.T) *Ledger {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewLedger(st.DB())
}

// A new sidecar at the same path supersedes the old row: one live row per path, the old
// one kept as history.
func TestLedgerRecordAndSupersede(t *testing.T) {
	l := ledgerFixture(t)
	ctx := context.Background()
	idx := 2
	first, err := l.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", MovieID: 1, VideoPath: "/m/a.mkv",
		Path: "/m/a.en.srt", Lang: "EN", Source: SourceEmbedded, EmbeddedIndex: &idx})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := l.LiveFile(ctx, "/m/a.en.srt")
	if err != nil || !ok || got.ID != first || got.Lang != "en" || got.Variant != VariantFull || got.EmbeddedIndex == nil || *got.EmbeddedIndex != 2 {
		t.Fatalf("live row = %+v ok=%v err=%v", got, ok, err)
	}
	score := 87
	second, err := l.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", MovieID: 1, VideoPath: "/m/a.mkv",
		Path: "/m/a.en.srt", Lang: "en", Source: SourceOpenSubtitles, Provider: "opensubtitles", ProviderFileID: "42",
		ReleaseName: "A.2020.1080p.BluRay-GRP", HashMatch: true, Score: &score})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, _ = l.LiveFile(ctx, "/m/a.en.srt")
	if !ok || got.ID != second || got.Source != SourceOpenSubtitles || got.ProviderFileID != "42" || !got.HashMatch || got.Score == nil || *got.Score != 87 || got.EmbeddedIndex != nil {
		t.Fatalf("after replace, live row = %+v", got)
	}
	var superseded int
	if err := l.db.QueryRow(`SELECT count(*) FROM subtitle_files WHERE path = ? AND superseded_at IS NOT NULL`, "/m/a.en.srt").Scan(&superseded); err != nil || superseded != 1 {
		t.Errorf("superseded rows = %d (%v), want 1", superseded, err)
	}

	// An SDH sidecar beside it is its own row; FilesFor lists the live ones.
	if _, err := l.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", MovieID: 1, VideoPath: "/m/a.mkv",
		Path: "/m/a.en.sdh.srt", Lang: "en", Variant: VariantSDH, Source: SourceAITranscribe, Model: "ggml-large-v3-turbo.bin"}); err != nil {
		t.Fatal(err)
	}
	files, err := l.FilesFor(ctx, "m:1")
	if err != nil || len(files) != 2 || files[0].Variant != VariantFull || files[1].Variant != VariantSDH || files[1].Model == "" {
		t.Fatalf("FilesFor = %+v, %v", files, err)
	}

	if ok, err := l.SetSync(ctx, "/m/a.en.srt", -350, 91); err != nil || !ok {
		t.Fatalf("SetSync = %v, %v", ok, err)
	}
	got, _, _ = l.LiveFile(ctx, "/m/a.en.srt")
	if got.SyncOffsetMs == nil || *got.SyncOffsetMs != -350 || got.SyncConfidence == nil || *got.SyncConfidence != 91 {
		t.Errorf("sync not stored: %+v", got)
	}

	if err := l.SupersedeFile(ctx, "/m/a.en.srt"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := l.LiveFile(ctx, "/m/a.en.srt"); ok {
		t.Error("a superseded path still has a live row")
	}
}

// Every language column holds the canonical code, whatever spelling came in, so rows
// written from a track tag ("eng") and from a kept language ("en") are one language.
func TestLedgerStoresCanonicalLanguages(t *testing.T) {
	l := ledgerFixture(t)
	ctx := context.Background()
	if _, err := l.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", VideoPath: "/v.mkv", Path: "/v.pt-br.srt", Lang: "pt_BR", Source: SourceEmbedded}); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordAttempts(ctx, "r", "m:1", "/v.mkv", []Outcome{{Lang: "eng", Rung: RungAI, Result: ResultOK}}); err != nil {
		t.Fatal(err)
	}
	if err := l.SaveBackoff(ctx, Backoff{MediaKey: "m:1", Lang: "fre", VideoPath: "/v.mkv", PrimaryReason: ResultNoMatch, Count: 1}); err != nil {
		t.Fatal(err)
	}
	f, _, _ := l.LiveFile(ctx, "/v.pt-br.srt")
	a, _ := l.Attempts(ctx, AttemptQuery{})
	bs, _ := l.LoadBackoffs(ctx)
	_, okFr := bs[BackoffKey("m:1", "fr", VariantFull)]
	if f.Lang != "pt-br" || len(a) != 1 || a[0].Lang != "en" || !okFr {
		t.Errorf("file lang %q, attempt %+v, backoff keys %v", f.Lang, a, keysOf(bs))
	}
}

// Specials are season 0: an episode keeps it rather than reading back as "no season".
func TestLedgerEpisodeSeasonZero(t *testing.T) {
	l := ledgerFixture(t)
	ctx := context.Background()
	if _, err := l.RecordFile(ctx, SubtitleFile{MediaKey: "e:7:0:3", Kind: "episode", SeriesID: 7, Season: 0, Episode: 3,
		VideoPath: "/tv/s00e03.mkv", Path: "/tv/s00e03.en.srt", Lang: "en", Source: SourceEmbedded}); err != nil {
		t.Fatal(err)
	}
	var season any
	if err := l.db.QueryRow(`SELECT season FROM subtitle_files`).Scan(&season); err != nil || season == nil {
		t.Errorf("season = %v (%v), want 0 not NULL", season, err)
	}
}

func TestLedgerBlocklist(t *testing.T) {
	l := ledgerFixture(t)
	ctx := context.Background()
	for _, e := range []BlockEntry{
		{Provider: "opensubtitles", FileID: "10", MediaKey: "m:1", Lang: "en", Reason: BlockRedo},
		{Provider: "opensubtitles", FileID: "11", MediaKey: "m:1", Lang: "en", Reason: BlockOutOfSync},
		{Provider: "opensubtitles", FileID: "10", MediaKey: "m:2", Lang: "en", Reason: BlockManual},
	} {
		if err := l.Block(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	// Blocking again updates the row instead of failing.
	if err := l.Block(ctx, BlockEntry{Provider: "opensubtitles", FileID: "10", MediaKey: "m:1", Lang: "en", Reason: BlockManual}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Blocklisted(ctx, "opensubtitles", "m:1")
	if err != nil || len(got) != 2 || !got["10"] || !got["11"] {
		t.Fatalf("Blocklisted(m:1) = %v, %v", got, err)
	}
	if got, _ := l.Blocklisted(ctx, "opensubtitles", "m:3"); len(got) != 0 {
		t.Errorf("an unrelated video has blocked files: %v", got)
	}
	rows, err := l.BlockedFor(ctx, "m:1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("BlockedFor = %+v, %v", rows, err)
	}
	if err := l.Unblock(ctx, "opensubtitles", "10", "m:1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.Blocklisted(ctx, "opensubtitles", "m:1"); got["10"] || !got["11"] {
		t.Errorf("after Unblock: %v", got)
	}
	// Without a database nothing is recorded, and blocking says so rather than pretending.
	var nl *Ledger
	if err := nl.Block(ctx, BlockEntry{Provider: "opensubtitles", FileID: "1", MediaKey: "m:1"}); err == nil {
		t.Error("Block on a nil ledger reported success")
	}
	if got, err := nl.Blocklisted(ctx, "opensubtitles", "m:1"); err != nil || len(got) != 0 {
		t.Errorf("nil ledger Blocklisted = %v, %v", got, err)
	}
}

func TestLedgerAttemptsPageAndPrune(t *testing.T) {
	l := ledgerFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return base }
	for i := range 5 {
		outs := []Outcome{
			{Lang: "en", Rung: RungOpenSubtitles, Result: ResultNoMatch, Detail: "no OpenSubtitles match"},
			{Lang: "en", Rung: RungAI, Result: ResultOK, Detail: "AI-generated"},
		}
		if err := l.RecordAttempts(ctx, fmt.Sprintf("run-%d", i), "m:1", "/m/a.mkv", outs); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RecordAttempts(ctx, "other", "m:2", "/m/b.mkv", []Outcome{{Lang: "es", Rung: RungAI, Result: ResultImpossible}}); err != nil {
		t.Fatal(err)
	}
	page, err := l.Attempts(ctx, AttemptQuery{MediaKey: "m:1", Limit: 4})
	if err != nil || len(page) != 4 {
		t.Fatalf("page 1 = %d rows, %v", len(page), err)
	}
	if page[0].RunID != "run-4" || page[0].Rung != RungAI || page[0].Outcome != ResultOK || page[0].Variant != VariantFull {
		t.Errorf("newest = %+v", page[0])
	}
	rest, _ := l.Attempts(ctx, AttemptQuery{MediaKey: "m:1", Before: page[3].ID, Limit: 100})
	if len(rest) != 6 {
		t.Errorf("page 2 = %d rows, want 6", len(rest))
	}
	if run, _ := l.Attempts(ctx, AttemptQuery{RunID: "run-2"}); len(run) != 2 {
		t.Errorf("run filter = %d rows, want 2", len(run))
	}

	// 180 days on, the old rows go; a new one stays.
	l.now = func() time.Time { return base.Add(AttemptRetention + time.Hour) }
	if err := l.RecordAttempts(ctx, "new", "m:1", "/m/a.mkv", []Outcome{{Lang: "en", Rung: RungAI, Result: ResultOK}}); err != nil {
		t.Fatal(err)
	}
	n, err := l.PruneAttempts(ctx, l.now().Add(-AttemptRetention))
	if err != nil || n != 11 {
		t.Errorf("pruned %d (%v), want 11", n, err)
	}
	if left, _ := l.Attempts(ctx, AttemptQuery{}); len(left) != 1 || left[0].RunID != "new" {
		t.Errorf("left after prune = %+v", left)
	}
}

// Each provider failure becomes its typed outcome, the job state follows the rule (done if
// anything was written, failed when every language ended in an error, skipped otherwise),
// and the note names the reason.
func TestProcessOutcomeClassification(t *testing.T) {
	cases := []struct {
		name      string
		searchErr error
		ai        *fakeAI
		result    Result
		state     JobState
		note      string
	}{
		{"login rejected, no AI", &authError{what: "login", status: 401}, &fakeAI{}, ResultAuthFailed, StateFailed,
			"en: OpenSubtitles login rejected (401) → no whisper model installed"},
		{"login rejected, AI makes it", &authError{what: "login", status: 401}, &fakeAI{avail: true, transcribe: true}, ResultAuthFailed, StateDone,
			"en: OpenSubtitles login rejected (401) → AI-generated"},
		{"quota", ErrQuotaExhausted, &fakeAI{}, ResultQuota, StateFailed, "en: quota spent → no whisper model installed"},
		{"provider error", errors.New("opensubtitles search: HTTP 500"), &fakeAI{avail: true, transcribe: true, fail: errors.New("exit 1")}, ResultProviderError, StateFailed,
			"en: OpenSubtitles error → AI failed: exit 1"},
		{"no match, can't translate", nil, &fakeAI{avail: true, transcribe: true}, ResultNoMatch, StateSkipped, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prov := &fakeProvider{canDownload: true, searchErr: map[string]error{"en": c.searchErr}}
			mi := englishAudio()
			if c.result == ResultNoMatch {
				// French audio and Spanish wanted: whisper can't make it.
				mi = &mediaInfo{AudioLangs: []string{"fre"}, Audio: []AudioTrack{{Index: 0, Lang: "fre"}}}
			}
			lang := "en"
			if c.result == ResultNoMatch {
				lang = "es"
				prov.searchErr = nil
			}
			s, _ := ladderFixture(t, lang, mi, prov, c.ai)
			job := runJob(s)
			if job.State != c.state {
				t.Errorf("state = %q, want %q (note %q)", job.State, c.state, job.Note)
			}
			if c.note != "" && job.Note != c.note {
				t.Errorf("note = %q, want %q", job.Note, c.note)
			}
			if len(job.Outcomes) < 2 || job.Outcomes[0].Rung != RungOpenSubtitles || job.Outcomes[0].Result != c.result {
				t.Fatalf("outcomes = %+v, want the download rung's %s first", job.Outcomes, c.result)
			}
			if job.RunID == "" {
				t.Fatal("job has no run id")
			}
			rows, err := s.ledger.Attempts(context.Background(), AttemptQuery{RunID: job.RunID})
			if err != nil || len(rows) != len(job.Outcomes) {
				t.Fatalf("ledger has %d attempts (%v), want %d", len(rows), err, len(job.Outcomes))
			}
			if rows[len(rows)-1].Outcome != c.result || rows[len(rows)-1].MediaKey != "m:1" {
				t.Errorf("first attempt row = %+v", rows[len(rows)-1])
			}
		})
	}
}

// A rejected login is said once: the other languages get the same outcome without
// another request.
func TestProcessAuthFailedStopsFurtherSearches(t *testing.T) {
	bad := &authError{what: "login", status: 401}
	prov := &fakeProvider{canDownload: true, searchErr: map[string]error{"en": bad, "es": bad}}
	mi := &mediaInfo{AudioLangs: []string{"eng", "spa"}, Audio: []AudioTrack{{Index: 0, Lang: "eng"}, {Index: 1, Lang: "spa"}}}
	s, _ := ladderFixture(t, "en,es", mi, prov, &fakeAI{})
	job := runJob(s)
	if prov.searches != 1 {
		t.Errorf("searches = %d, want 1", prov.searches)
	}
	if job.State != StateFailed || !strings.Contains(job.Note, "es: OpenSubtitles login rejected (401)") {
		t.Errorf("state=%q note=%q", job.State, job.Note)
	}
}

// Every sidecar a job writes gets a provenance row with its real source.
func TestProcessRecordsProvenance(t *testing.T) {
	prov := &fakeProvider{canDownload: true, results: map[string][]SubtitleResult{
		"es": {{FileID: "777", Release: "Movie.2020.1080p.WEB-DL-GRP", HashMatch: true}},
	}}
	ai := &fakeAI{avail: true, transcribe: true, translate: true}
	mi := &mediaInfo{AudioLangs: []string{"eng"}, Audio: []AudioTrack{{Index: 0, Lang: "eng"}},
		Subs: []SubTrack{{Index: 3, Codec: "subrip", Lang: "fre", Text: true}}}
	s, video := ladderFixture(t, "fr,es,en", mi, prov, ai)
	s.extract = func(_ context.Context, _ string, picks []extractPick) error {
		for _, p := range picks {
			if err := os.WriteFile(p.Path, []byte(fakeSRT), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	prov.results["en"] = nil // no match: AI transcribes it
	job := runJob(s)
	if job.State != StateDone {
		t.Fatalf("state = %q note=%q", job.State, job.Note)
	}
	files, err := s.ledger.FilesFor(context.Background(), "m:1")
	if err != nil || len(files) != 3 {
		t.Fatalf("files = %+v, %v", files, err)
	}
	by := map[string]SubtitleFile{}
	for _, f := range files {
		by[f.Lang] = f
		if f.VideoPath != video || f.Kind != "movie" || f.MovieID != 1 {
			t.Errorf("identity not filled: %+v", f)
		}
	}
	if f := by["fr"]; f.Source != SourceEmbedded || f.EmbeddedIndex == nil || *f.EmbeddedIndex != 3 {
		t.Errorf("fr = %+v, want embedded track 3", f)
	}
	if f := by["es"]; f.Source != SourceOpenSubtitles || f.Provider != "opensubtitles" || f.ProviderFileID != "777" || !f.HashMatch || f.ReleaseName == "" {
		t.Errorf("es = %+v, want the download's file id, release and hash match", f)
	}
	if f := by["en"]; f.Source != SourceAITranscribe {
		t.Errorf("en = %+v, want ai-transcribe", f)
	}
}

// The job's outcomes travel in the jobs JSON for the Queue tab and the persistent queue.
func TestJobOutcomesJSON(t *testing.T) {
	j := Job{ID: 1, Kind: "movie", MovieID: 1, Title: "M", State: StateFailed, RunID: "r1",
		Outcomes: []Outcome{{Lang: "en", Rung: RungOpenSubtitles, Result: ResultAuthFailed, Detail: "OpenSubtitles login rejected (401)"}}}
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	outs, _ := back["outcomes"].([]any)
	if back["run_id"] != "r1" || len(outs) != 1 {
		t.Fatalf("json = %s", raw)
	}
	o := outs[0].(map[string]any)
	if o["lang"] != "en" || o["rung"] != "opensubtitles" || o["result"] != "auth_failed" || o["detail"] == nil {
		t.Errorf("outcome json = %v", o)
	}
	if _, has := o["variant"]; has {
		t.Errorf("full variant should be omitted: %v", o)
	}
}

// The download rung reads the ledger's blocklist (a blocked file is never fetched again
// for that video), stores the saved candidate's score, and types a refused candidate as
// below_score with its explanation, falling through to AI.
func TestDownloadUsesLedgerBlocklistAndRecordsScore(t *testing.T) {
	prov := &fakeProvider{canDownload: true, results: map[string][]SubtitleResult{
		"en": {{FileID: "1", HashMatch: true, Downloads: 900}, {FileID: "2", HashMatch: true, Downloads: 5}},
	}}
	s, _ := ladderFixture(t, "en", englishAudio(), prov, &fakeAI{})
	s.blocklist = s.ledgerBlocklist
	ctx := context.Background()
	if err := s.ledger.Block(ctx, BlockEntry{Provider: "opensubtitles", FileID: "1", MediaKey: "m:1", Lang: "en", Reason: BlockRedo}); err != nil {
		t.Fatal(err)
	}
	job := runJob(s)
	if job.State != StateDone {
		t.Fatalf("state=%q note=%q", job.State, job.Note)
	}
	files, _ := s.ledger.FilesFor(ctx, "m:1")
	if len(files) != 1 || files[0].ProviderFileID != "2" || files[0].Score == nil || *files[0].Score < 100 {
		t.Fatalf("files = %+v, want file 2 (1 is blocked) with its score", files)
	}
	rows, _ := s.ledger.Attempts(ctx, AttemptQuery{RunID: job.RunID})
	if len(rows) != 1 || rows[0].Outcome != ResultOK || !strings.Contains(rows[0].Detail, "score ") {
		t.Errorf("attempts = %+v, want the score in the detail", rows)
	}

	// A blocklist that can't be read downloads nothing (fail closed).
	prov2 := &fakeProvider{canDownload: true, results: map[string][]SubtitleResult{"en": {{FileID: "3", HashMatch: true}}}}
	s2, video2 := ladderFixture(t, "en", englishAudio(), prov2, &fakeAI{})
	s2.blocklist = func(context.Context, string) (map[string]bool, error) { return nil, errors.New("disk I/O error") }
	job = runJob(s2)
	if _, err := os.Stat(sidecarPath(video2, "en")); err == nil {
		t.Error("a subtitle was downloaded while the blocklist couldn't be read")
	}
	if len(job.Outcomes) == 0 || job.Outcomes[0].Result != ResultProviderError {
		t.Errorf("outcomes = %+v", job.Outcomes)
	}

	// No hash match and nothing in common with the file: refused as below_score, then AI.
	prov3 := &fakeProvider{canDownload: true, results: map[string][]SubtitleResult{"en": {{FileID: "4", Release: "Other.Film.1999.720p.HDTV-XYZ"}}}}
	s3, _ := ladderFixture(t, "en", englishAudio(), prov3, &fakeAI{avail: true, transcribe: true})
	job = runJob(s3)
	if len(job.Outcomes) != 2 || job.Outcomes[0].Result != ResultBelowScore || job.Outcomes[1].Result != ResultOK {
		t.Fatalf("outcomes = %+v, want below_score then AI", job.Outcomes)
	}
	rows, _ = s3.ledger.Attempts(ctx, AttemptQuery{RunID: job.RunID})
	if len(rows) != 2 || rows[1].Outcome != ResultBelowScore || !strings.Contains(rows[1].Detail, "below") {
		t.Errorf("attempts = %+v", rows)
	}
}
