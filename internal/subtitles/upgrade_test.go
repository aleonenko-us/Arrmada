package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/store"
)

// upgradeProvider is OpenSubtitles for the upgrade loop: per-language results, files by
// id, a download allowance, and a pause switch. It counts searches and downloads.
type upgradeProvider struct {
	mu        sync.Mutex
	results   map[string][]SubtitleResult
	files     map[string]string
	remaining int // -1 = never reported
	paused    bool
	searches  []string
	downloads []string
}

func (p *upgradeProvider) Available() bool   { return true }
func (p *upgradeProvider) CanDownload() bool { return true }
func (p *upgradeProvider) Paused() bool      { return p.paused }
func (p *upgradeProvider) Quota() (int, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remaining, time.Time{}
}
func (p *upgradeProvider) Search(_ context.Context, r SearchRequest) ([]SubtitleResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searches = append(p.searches, r.Language)
	return p.results[r.Language], nil
}
func (p *upgradeProvider) Download(_ context.Context, id string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloads = append(p.downloads, id)
	if p.remaining > 0 {
		p.remaining--
	}
	body, ok := p.files[id]
	if !ok {
		body = "1\n00:00:01,000 --> 00:00:02,000\nfrom " + id + "\n"
	}
	return []byte(body), nil
}

// upgradeFixture is a movie with a recycle bin, the provider, and the title's profile.
func upgradeFixture(t *testing.T, langs string, prov Provider) (*Service, string, string) {
	t.Helper()
	mi := englishAudio()
	mi.FPS = 23.976
	s, video := ladderFixture(t, langs, mi, prov, &fakeAI{})
	s.resolve = func(context.Context, *Job) (fileRef, bool) {
		return fileRef{Path: video, Title: "Movie", Year: 2020, MediaKey: "m:1", SourceRelease: bluray2160}, true
	}
	bin := t.TempDir()
	s.bin = library.SingleBin(bin)
	s.blocklist = s.ledgerBlocklist
	return s, video, bin
}

// runUpgrade runs the daily pass and then every job it queued, in order.
func runUpgrade(t *testing.T, s *Service) (int, []*Job) {
	t.Helper()
	n := s.UpgradeSweep(context.Background())
	var jobs []*Job
	for j := s.pop(); j != nil; j = s.pop() {
		s.process(context.Background(), j)
		jobs = append(jobs, j)
	}
	return n, jobs
}

func aiMade(lang string) *SubtitleFile {
	return &SubtitleFile{Lang: lang, Source: SourceAITranscribe, Model: "ggml-large-v3-turbo.bin"}
}

func hashMatch(id string) SubtitleResult { return SubtitleResult{FileID: id, HashMatch: true} }

func TestUpgradeCandidatesQuery(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	l := NewLedger(st.DB())
	ctx := context.Background()
	now := time.Now()
	day := 24 * time.Hour
	score := 70
	add := func(name string, f SubtitleFile) {
		f.Path, f.MediaKey, f.Kind, f.VideoPath = "/m/"+name, "m:1", "movie", "/m/movie.mkv"
		if f.Lang == "" {
			f.Lang = "en"
		}
		if f.CreatedAt == 0 {
			f.CreatedAt = now.Add(-2 * day).Unix()
		}
		if _, err := l.RecordFile(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	add("download.en.srt", SubtitleFile{Source: SourceOpenSubtitles, Score: &score, CreatedAt: now.Add(-3 * day).Unix()})
	add("ai.es.srt", SubtitleFile{Lang: "es", Source: SourceAITranscribe})
	add("translated.fr.srt", SubtitleFile{Lang: "fr", Source: SourceAITranslate})
	add("old-ai.de.srt", SubtitleFile{Lang: "de", Source: SourceAITranscribe, CreatedAt: now.Add(-22 * day).Unix()})
	add("hash.it.srt", SubtitleFile{Lang: "it", Source: SourceOpenSubtitles, HashMatch: true})
	add("locked.pt.srt", SubtitleFile{Lang: "pt", Source: SourceOpenSubtitles, Locked: true})
	add("forced.ja.srt", SubtitleFile{Lang: "ja", Variant: VariantForced, Source: SourceOpenSubtitles})
	for _, src := range []string{SourceAdopted, SourceManual, SourceUnknown, SourceRelease, SourceEmbedded} {
		add(src+".nl.srt", SubtitleFile{Lang: "nl", Source: src})
	}
	// Superseded: the path's newer row (a hash-matched download) replaced it.
	add("replaced.sv.srt", SubtitleFile{Lang: "sv", Source: SourceAITranscribe})
	add("replaced.sv.srt", SubtitleFile{Lang: "sv", Source: SourceOpenSubtitles, HashMatch: true})

	rows, err := l.UpgradeCandidates(ctx, now.Add(-21*day))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range rows {
		got = append(got, filepath.Base(f.Path))
	}
	// AI-made first, then the weak download; nothing else.
	if want := "ai.es.srt,translated.fr.srt,download.en.srt"; strings.Join(got, ",") != want {
		t.Errorf("candidates = %v, want %s", got, want)
	}
}

// An AI subtitle is switched to a hash-matched download once one exists; the AI file is in
// the recycle bin, the ledger says where the new one came from, and the week's count shows it.
func TestUpgradeReplacesOnHashMatch(t *testing.T) {
	prov := &upgradeProvider{remaining: -1, results: map[string][]SubtitleResult{"en": {candidate("loose"), hashMatch("exact")}}}
	s, video, bin := upgradeFixture(t, "en", prov)
	ctx := context.Background()
	old := writeOld(t, s, video, "Movie (2020).en.srt", "ai words", aiMade("en"))

	n, jobs := runUpgrade(t, s)
	if n != 1 || len(jobs) != 1 || jobs[0].State != StateDone {
		t.Fatalf("queued %d, jobs %+v", n, jobs)
	}
	if strings.Join(prov.downloads, ",") != "exact" {
		t.Errorf("downloads = %v, want only the hash match", prov.downloads)
	}
	if raw, _ := os.ReadFile(old); !strings.Contains(string(raw), "from exact") {
		t.Errorf("sidecar now %q", raw)
	}
	if !binHolds(t, bin, "Movie (2020).en.srt") {
		t.Error("the AI subtitle isn't in the recycle bin")
	}
	f, ok, _ := s.ledger.LiveFile(ctx, old)
	if !ok || f.Source != SourceOpenSubtitles || !f.HashMatch || f.ProviderFileID != "exact" || f.Score == nil || *f.Score != 100 {
		t.Errorf("live row %+v", f)
	}
	atts, _ := s.ledger.Attempts(ctx, AttemptQuery{MediaKey: "m:1"})
	if len(atts) != 1 || atts[0].Rung != RungUpgrade || atts[0].Outcome != ResultOK {
		t.Errorf("attempts %+v", atts)
	}
	if got := s.GetSettings(ctx).UpgradedWeek; got != 1 {
		t.Errorf("upgraded this week = %d, want 1", got)
	}
	// A hash-matched subtitle is as good as it gets: never looked at again.
	if n, _ := runUpgrade(t, s); n != 0 {
		t.Errorf("a second pass queued %d jobs", n)
	}
}

// The loop spends at most its share of the day's remaining downloads, and stops the job's
// other languages when the share is used.
func TestUpgradeRespectsQuotaShare(t *testing.T) {
	langs := []string{"en", "es", "fr", "de", "it"}
	prov := &upgradeProvider{remaining: 10, results: map[string][]SubtitleResult{}}
	for _, l := range langs {
		prov.results[l] = []SubtitleResult{hashMatch("exact-" + l)}
	}
	s, video, _ := upgradeFixture(t, strings.Join(langs, ","), prov)
	for _, l := range langs {
		writeOld(t, s, video, "Movie (2020)."+l+".srt", "ai words", aiMade(l))
	}
	_, jobs := runUpgrade(t, s)
	if len(jobs) != 1 {
		t.Fatalf("jobs %+v", jobs)
	}
	if len(prov.downloads) != 3 { // 30% of 10
		t.Errorf("downloads = %v, want 3", prov.downloads)
	}
	ok, quota := 0, 0
	for _, o := range jobs[0].Outcomes {
		switch o.Result {
		case ResultOK:
			ok++
		case ResultQuota:
			quota++
		}
	}
	if ok != 3 || quota != 1 || len(prov.searches) != 4 {
		t.Errorf("outcomes %+v, searches %v — want 3 upgraded, then one stopped by the share and no more searches", jobs[0].Outcomes, prov.searches)
	}
	// With 2 downloads left the share rounds to none: nothing is even searched.
	prov.mu.Lock()
	prov.remaining, prov.searches = 2, nil
	prov.mu.Unlock()
	if n := s.UpgradeSweep(context.Background()); n != 0 || len(prov.searches) != 0 {
		t.Errorf("queued %d with no downloads to spare (searches %v)", n, prov.searches)
	}
}

// Locked (the owner's pick), adopted, release and unowned subtitles are never replaced,
// whatever turns up.
func TestUpgradeLeavesOwnersChoicesAlone(t *testing.T) {
	prov := &upgradeProvider{remaining: -1, results: map[string][]SubtitleResult{}}
	for _, l := range []string{"en", "es", "fr", "de"} {
		prov.results[l] = []SubtitleResult{hashMatch("exact-" + l)}
	}
	s, video, _ := upgradeFixture(t, "en,es,fr,de", prov)
	paths := []string{
		writeOld(t, s, video, "Movie (2020).en.srt", "picked", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, Provider: providerOpenSubtitles, ProviderFileID: "p", Locked: true}),
		writeOld(t, s, video, "Movie (2020).es.srt", "adopted", &SubtitleFile{Lang: "es", Source: SourceAdopted}),
		writeOld(t, s, video, "Movie (2020).fr.srt", "release", &SubtitleFile{Lang: "fr", Source: SourceRelease}),
		writeOld(t, s, video, "Movie (2020).de.srt", "not ours", nil),
	}
	if n, jobs := runUpgrade(t, s); n != 0 || len(jobs) != 0 || len(prov.searches) != 0 {
		t.Fatalf("queued %d, searched %v", n, prov.searches)
	}
	for _, p := range paths {
		if raw, _ := os.ReadFile(p); strings.Contains(string(raw), "from ") {
			t.Errorf("%s was replaced", filepath.Base(p))
		}
	}
	// A job for a locked row (queued before the owner picked it) still leaves it alone.
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Mode: ModeUpgrade, Langs: []string{"en"}, Priority: PrioSweep})
	s.pop()
	s.process(context.Background(), job)
	if len(prov.downloads) != 0 || job.State != StateSkipped {
		t.Errorf("state %q note %q downloads %v", job.State, job.Note, prov.downloads)
	}
}

// Nothing is searched while OpenSubtitles is paused (quota spent, login rejected).
func TestUpgradeSkipsWhilePaused(t *testing.T) {
	prov := &upgradeProvider{remaining: -1, paused: true, results: map[string][]SubtitleResult{"en": {hashMatch("exact")}}}
	s, video, _ := upgradeFixture(t, "en", prov)
	writeOld(t, s, video, "Movie (2020).en.srt", "ai words", aiMade("en"))
	if n, _ := runUpgrade(t, s); n != 0 || len(prov.searches) != 0 {
		t.Errorf("queued %d, searched %v while paused", n, prov.searches)
	}
}

// With nothing better, the attempt says when the next look is, and the file isn't searched
// again until then.
func TestUpgradeRechecksOnSchedule(t *testing.T) {
	prov := &upgradeProvider{remaining: -1, results: map[string][]SubtitleResult{}}
	s, video, _ := upgradeFixture(t, "en", prov)
	writeOld(t, s, video, "Movie (2020).en.srt", "ai words", aiMade("en"))
	_, jobs := runUpgrade(t, s)
	if len(jobs) != 1 || jobs[0].State != StateSkipped || len(jobs[0].Outcomes) != 1 || jobs[0].Outcomes[0].Result != ResultNoMatch ||
		!strings.Contains(jobs[0].Outcomes[0].Detail, "next look") {
		t.Fatalf("jobs %+v", jobs)
	}
	if n, _ := runUpgrade(t, s); n != 0 || len(prov.searches) != 1 {
		t.Errorf("searched again straight away: queued %d, searches %v", n, prov.searches)
	}
	created := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	if got := nextUpgradeCheck(created, created.Add(24*time.Hour)); got != created.Add(44*time.Hour) {
		t.Errorf("first week: next check %v", got)
	}
	if got := nextUpgradeCheck(created, created.Add(10*24*time.Hour)); got != created.Add(10*24*time.Hour+68*time.Hour) {
		t.Errorf("after a week: next check %v", got)
	}
}

// A download without a hash match replaces an AI subtitle only when it fits the audio
// with confidence ≥ 80; one that doesn't is refused and never fetched again.
func TestUpgradeNonHashNeedsConfidentSync(t *testing.T) {
	speech := synthSpeech(8, 4*time.Minute) // what syncFixture's audio says
	unrelated := formatSRT(subtitleFor(synthSpeech(909, 4*time.Minute), 3))
	prov := &upgradeProvider{remaining: -1,
		results: map[string][]SubtitleResult{"en": {candidate("fits")}, "es": {candidate("unrelated")}},
		files:   map[string]string{"fits": formatSRT(subtitleFor(speech, 5)), "unrelated": unrelated},
	}
	s, video, _, _, _ := syncFixture(t, prov, &fakeAI{})
	bin := t.TempDir()
	s.bin = library.SingleBin(bin)
	s.blocklist = s.ledgerBlocklist
	en := writeOld(t, s, video, "Movie (2020).en.srt", "ai words", aiMade("en"))
	es := writeOld(t, s, video, "Movie (2020).es.srt", "ai palabras", aiMade("es"))

	_, jobs := runUpgrade(t, s)
	if len(jobs) != 1 || jobs[0].State != StateDone {
		t.Fatalf("jobs %+v", jobs)
	}
	ctx := context.Background()
	if f, ok, _ := s.ledger.LiveFile(ctx, en); !ok || f.ProviderFileID != "fits" || f.SyncConfidence == nil || *f.SyncConfidence < upgradeSyncConfidence {
		t.Errorf("en row %+v", f)
	}
	if raw, _ := os.ReadFile(es); string(raw) != "ai palabras" {
		t.Errorf("the AI Spanish subtitle was replaced by one that doesn't fit: %q", raw)
	}
	if f, ok, _ := s.ledger.LiveFile(ctx, es); !ok || f.Source != SourceAITranscribe {
		t.Errorf("es row %+v", f)
	}
	blocked, _ := s.ledger.Blocklisted(ctx, providerOpenSubtitles, "m:1")
	if !blocked["unrelated"] || blocked["fits"] {
		t.Errorf("blocklist %v", blocked)
	}
	var esOut Outcome
	for _, o := range jobs[0].Outcomes {
		if o.Lang == "es" {
			esOut = o
		}
	}
	if esOut.Result != ResultOutOfSync {
		t.Errorf("es outcome %+v", esOut)
	}
}

// betterCandidate: a hash match always; otherwise only something that outscores the
// current subtitle, and never the very file it is.
func TestBetterCandidate(t *testing.T) {
	score := 70
	weak := SubtitleFile{Source: SourceOpenSubtitles, ProviderFileID: "cur", Score: &score}
	rank := func(cs ...ScoredCandidate) []ScoredCandidate { return cs }
	sc := func(id string, n int) ScoredCandidate {
		return ScoredCandidate{SubtitleResult: SubtitleResult{FileID: id}, Score: n}
	}
	cases := []struct {
		name   string
		ranked []ScoredCandidate
		row    SubtitleFile
		want   string
	}{
		{"hash match", rank(ScoredCandidate{SubtitleResult: SubtitleResult{FileID: "h", HashMatch: true}, Score: 100}), weak, "h"},
		{"outscores", rank(sc("cur", 80), sc("b", 75)), weak, "b"},
		{"no better", rank(sc("b", 70), sc("c", 60)), weak, ""},
		{"any match beats AI", rank(sc("b", 40)), *aiMade("en"), "b"},
		{"below the floor", rank(sc("b", scoreFloor-1)), *aiMade("en"), ""},
		{"rejected", rank(ScoredCandidate{SubtitleResult: SubtitleResult{FileID: "r"}, Score: 90, Rejected: "machine-translated"}), weak, ""},
	}
	for _, c := range cases {
		got, ok := betterCandidate(c.ranked, c.row)
		if (c.want == "") == ok || (ok && got.FileID != c.want) {
			t.Errorf("%s: got %q (%v), want %q", c.name, got.FileID, ok, c.want)
		}
	}

}
