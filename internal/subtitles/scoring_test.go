package subtitles

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

const bluray2160 = "Movie.2020.2160p.BluRay.x265-GRP"

func TestScoreCandidate(t *testing.T) {
	film := MatchContext{SourceRelease: bluray2160, FileBase: "Movie (2020)", FPS: 23.976023976}
	ep := MatchContext{SourceRelease: "Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-FLUX", FPS: 23.976, Season: 1, Episode: 2}
	cases := []struct {
		name   string
		c      SubtitleResult
		m      MatchContext
		score  int
		reject string
		reason string // one reason that must be listed
	}{
		{"hash match wins outright", SubtitleResult{HashMatch: true, Release: "anything", FPS: 25}, film, 100, "", "+100 hash match"},
		{"same release, same fps", SubtitleResult{Release: bluray2160, FPS: 23.976}, film, 75, "", "+25 same group (GRP)"},
		{"remux counts as Blu-ray", SubtitleResult{Release: "Movie.2020.2160p.BluRay.REMUX.HEVC-GRP", FPS: 23.976}, film, 75, "", "+20 same source (BluRay)"},
		{"WEBRip at 25 fps against a Blu-ray", SubtitleResult{Release: "Movie.2020.1080p.WEBRip.x264-OTHER", FPS: 25}, film, 0, "", "−40 25 vs 23.976 fps"},
		{"source mismatch alone", SubtitleResult{Release: "Movie.2020.2160p.WEBRip.x265-GRP"}, film, 25 - 15 + 5 + 10, "", "−15 WEBRip vs BluRay"},
		{"extended cut against theatrical", SubtitleResult{Release: "Movie.2020.Extended.2160p.BluRay.x265-GRP", FPS: 23.976}, film, 25 + 20 + 5 - 30 + 15, "", "−30 Extended vs theatrical"},
		{"same edition", SubtitleResult{Release: "Movie.2020.Extended.1080p.BluRay.x264-X"}, MatchContext{SourceRelease: "Movie.2020.EXTENDED.2160p.BluRay.x265-GRP"}, 20 + 10, "", "+10 same edition (Extended)"},
		{"release name empty, file name used", SubtitleResult{FileName: bluray2160 + ".srt", FPS: 23.976}, film, 75, "", "+5 same resolution"},
		{"trusted uploader", SubtitleResult{Release: "Movie.2020.1080p.BluRay.x264-OTHER", Trusted: true}, film, 20 + 10 + 5, "", "+5 trusted uploader"},
		{"unknown fps is neutral", SubtitleResult{Release: bluray2160}, film, 60, "", "+10 same edition"},
		{"bare file name says nothing", SubtitleResult{Release: bluray2160, FPS: 23.976}, MatchContext{FileBase: "Movie (2020)", FPS: 23.976}, 15, "", "+15 same frame rate"},
		{"episode mismatch", SubtitleResult{Release: "Show.S01E03.1080p.WEB-DL-FLUX", Season: 1, Episode: 3}, ep, 25 + 20 + 5 + 10, "for S01E03, not this episode", ""},
		{"right episode", SubtitleResult{Release: "Show.S01E02.1080p.WEB-DL-FLUX", Season: 1, Episode: 2, FPS: 23.976}, ep, 75, "", "+25 same group (FLUX)"},
		{"AI-translated", SubtitleResult{Release: bluray2160, AITranslated: true}, film, 60, "machine-translated", ""},
		{"foreign parts for a full want", SubtitleResult{Release: bluray2160, ForeignPartsOnly: true}, film, 60, "foreign parts only", ""},
	}
	for _, c := range cases {
		score, reasons := scoreCandidate(c.c, c.m)
		if score != c.score {
			t.Errorf("%s: score %d, want %d (%v)", c.name, score, c.score, reasons)
		}
		if got := rejectReason(c.c, c.m); got != c.reject {
			t.Errorf("%s: reject %q, want %q", c.name, got, c.reject)
		}
		if c.reason != "" && !hasReason(reasons, c.reason) {
			t.Errorf("%s: reasons %v lack %q", c.name, reasons, c.reason)
		}
	}
}

func hasReason(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestSearchParsesExtendedAttributes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"attributes":{
			"language":"en","release":"Show.S01E02.1080p.WEB-DL-FLUX","download_count":12,
			"fps":23.976,"from_trusted":true,"votes":3,
			"uploader":{"uploader_id":7,"name":"someone","rank":"trusted"},
			"feature_details":{"season_number":1,"episode_number":2,"title":"Show"},
			"files":[{"file_id":99,"cd_number":1,"file_name":"Show.S01E02.1080p.WEB-DL-FLUX.srt"}]}}]}`)
	}))
	defer srv.Close()
	o := NewOpenSubtitles("key", "", "")
	o.baseURL = srv.URL
	res, err := o.Search(context.Background(), SearchRequest{IMDBID: "tt1", Language: "en", Season: 1, Episode: 2})
	if err != nil || len(res) != 1 {
		t.Fatalf("results %+v err %v", res, err)
	}
	got := res[0]
	if got.FileID != "99" || got.FPS != 23.976 || !got.Trusted || got.UploaderRank != "trusted" ||
		got.FileName != "Show.S01E02.1080p.WEB-DL-FLUX.srt" || got.Season != 1 || got.Episode != 2 || got.Downloads != 12 {
		t.Errorf("parsed %+v", got)
	}
}

// pickProvider records which file ids were downloaded.
type pickProvider struct {
	mu        sync.Mutex
	results   []SubtitleResult
	downloads []string
}

func (p *pickProvider) Available() bool   { return true }
func (p *pickProvider) CanDownload() bool { return true }
func (p *pickProvider) Search(context.Context, SearchRequest) ([]SubtitleResult, error) {
	return p.results, nil
}
func (p *pickProvider) Download(_ context.Context, id string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloads = append(p.downloads, id)
	return []byte(fakeSRT), nil
}

func grabFixture(t *testing.T, results ...SubtitleResult) (*Service, *pickProvider, fileRef) {
	t.Helper()
	prov := &pickProvider{results: results}
	s, video := ladderFixture(t, "en", englishAudio(), prov, &fakeAI{})
	return s, prov, fileRef{Path: video, Title: "Movie", Year: 2020, MediaKey: "m:1", SourceRelease: bluray2160, FPS: 23.976}
}

// The closest release wins over the most-downloaded one.
func TestGrabOnePicksHighestScore(t *testing.T) {
	s, prov, ref := grabFixture(t,
		SubtitleResult{FileID: "popular", Release: "Movie.2020.1080p.WEBRip.x264-OTHER", FPS: 25, Downloads: 90000},
		SubtitleResult{FileID: "match", Release: "Movie.2020.2160p.BluRay.x265-GRP", Downloads: 40},
	)
	res, err := s.grabOne(context.Background(), ref, "", "en")
	if err != nil || !res.Written {
		t.Fatalf("res %+v err %v", res, err)
	}
	if len(prov.downloads) != 1 || prov.downloads[0] != "match" || res.Pick.Score != 60 {
		t.Errorf("downloaded %v, pick %+v", prov.downloads, res.Pick)
	}
	if _, err := os.Stat(sidecarPath(ref.Path, "en")); err != nil {
		t.Error(err)
	}
	// A hash match beats every name match.
	s, prov, ref = grabFixture(t,
		SubtitleResult{FileID: "match", Release: bluray2160, FPS: 23.976},
		SubtitleResult{FileID: "hash", HashMatch: true, Release: "whatever"},
	)
	if res, _ := s.grabOne(context.Background(), ref, "h", "en"); !res.Written || prov.downloads[0] != "hash" {
		t.Errorf("downloaded %v", prov.downloads)
	}
}

// A Blu-ray 2160p file with no hash match isn't given a 25 fps WEBRip subtitle: the job
// falls through to AI and says why.
func TestGrabOneBelowThresholdFallsThrough(t *testing.T) {
	prov := &pickProvider{results: []SubtitleResult{{FileID: "1", Release: "Movie.2020.1080p.WEBRip.x264-OTHER", FPS: 25, Downloads: 5000}}}
	ai := &fakeAI{avail: true, transcribe: true}
	mi := englishAudio()
	mi.FPS = 23.976023976
	s, video := ladderFixture(t, "en", mi, prov, ai)
	s.resolve = func(context.Context, *Job) (fileRef, bool) {
		return fileRef{Path: video, Title: "Movie", Year: 2020, MediaKey: "m:1", SourceRelease: bluray2160}, true
	}
	job := runJob(s)
	if len(prov.downloads) != 0 {
		t.Errorf("downloaded %v", prov.downloads)
	}
	want := "en: best OpenSubtitles match scored 0, below 60: WEBRip vs BluRay, 25 vs 23.976 fps → AI-generated"
	if job.State != StateDone || job.Note != want {
		t.Errorf("state=%q note=%q\nwant %q", job.State, job.Note, want)
	}
	assertSidecar(t, video, "en")
}

// The 35-to-minimum band waits for SUB-19's sync check: refused without it, and with it
// each candidate in the band is downloaded and checked, at most three, until one passes.
func TestGrabOneBandNeedsSyncCheck(t *testing.T) {
	band := func(id string) SubtitleResult {
		return SubtitleResult{FileID: id, Release: "Movie.2020.1080p.BluRay.x264-OTHER", FPS: 23.976} // 20+10+15 = 45
	}
	s, prov, ref := grabFixture(t, band("a"), band("b"), band("c"), band("d"))
	res, err := s.grabOne(context.Background(), ref, "", "en")
	if err != nil || res.Written || res.Outcome != "below_score" || len(prov.downloads) != 0 {
		t.Fatalf("no gate: res %+v downloads %v err %v", res, prov.downloads, err)
	}

	var checked []string
	s.syncGate = func(_ context.Context, _ fileRef, _ string, c ScoredCandidate, srt []byte) ([]byte, bool, string) {
		checked = append(checked, c.FileID)
		return []byte(strings.ReplaceAll(string(srt), "Hello.", "Shifted.")), c.FileID == "b", "out of sync"
	}
	res, err = s.grabOne(context.Background(), ref, "", "en")
	if err != nil || !res.Written || res.Pick.FileID != "b" || strings.Join(checked, ",") != "a,b" {
		t.Fatalf("gate: res %+v checked %v err %v", res, checked, err)
	}
	if raw, _ := os.ReadFile(res.Path); !strings.Contains(string(raw), "Shifted.") {
		t.Errorf("the gate's corrected bytes weren't saved: %q", raw)
	}

	checked = nil
	prov.downloads = nil
	s.syncGate = func(_ context.Context, _ fileRef, _ string, c ScoredCandidate, srt []byte) ([]byte, bool, string) {
		checked = append(checked, c.FileID)
		return nil, false, "out of sync by 40 s"
	}
	res, _ = s.grabOne(context.Background(), ref, "", "en")
	if res.Written || res.Outcome != "out_of_sync" || len(prov.downloads) != maxDownloadTries {
		t.Errorf("all refused: res %+v downloads %v", res, prov.downloads)
	}
}

func TestGrabOneSkipsBlocklisted(t *testing.T) {
	s, prov, ref := grabFixture(t,
		SubtitleResult{FileID: "bad", HashMatch: true},
		SubtitleResult{FileID: "good", Release: bluray2160, FPS: 23.976},
	)
	var askedFor string
	s.blocklist = func(_ context.Context, key string) (map[string]bool, error) {
		askedFor = key
		return map[string]bool{"bad": true}, nil
	}
	res, err := s.grabOne(context.Background(), ref, "h", "en")
	if err != nil || !res.Written || askedFor != "m:1" || strings.Join(prov.downloads, ",") != "good" {
		t.Fatalf("res %+v downloads %v key %q err %v", res, prov.downloads, askedFor, err)
	}

	// Everything blocklisted: nothing to download.
	s.blocklist = func(context.Context, string) (map[string]bool, error) {
		return map[string]bool{"bad": true, "good": true}, nil
	}
	prov.downloads = nil
	if res, _ := s.grabOne(context.Background(), ref, "h", "en"); res.Written || res.Outcome != "no_match" || len(prov.downloads) != 0 {
		t.Errorf("all blocklisted: %+v %v", res, prov.downloads)
	}

	// The blocklist can't be read: fail closed, download nothing.
	s.blocklist = func(context.Context, string) (map[string]bool, error) { return nil, errors.New("db locked") }
	if _, err := s.grabOne(context.Background(), ref, "h", "en"); err == nil || len(prov.downloads) != 0 {
		t.Errorf("blocklist error: err %v downloads %v", err, prov.downloads)
	}
}

// The minimum is read per grab: changing it applies to the very next one.
func TestMinScoreSettingAppliesNextGrab(t *testing.T) {
	s, prov, ref := grabFixture(t, SubtitleResult{FileID: "1", Release: "Movie.2020.1080p.BluRay.x264-OTHER", FPS: 23.976}) // 45
	ctx := context.Background()
	if s.minScore(ctx) != defaultMinScore {
		t.Fatalf("default min score %d", s.minScore(ctx))
	}
	if res, _ := s.grabOne(ctx, ref, "", "en"); res.Written {
		t.Fatal("45 accepted at the default minimum")
	}
	if err := s.SetMinScore(ctx, 40); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.grabOne(ctx, ref, "", "en"); !res.Written || len(prov.downloads) != 1 {
		t.Errorf("45 refused at minimum 40: %+v", res)
	}
	if err := s.SetMinScore(ctx, 101); !errors.Is(err, ErrBadMinScore) {
		t.Errorf("101 accepted: %v", err)
	}
	if s.minScore(ctx) != 40 {
		t.Errorf("min score %d after a refused change", s.minScore(ctx))
	}
}
