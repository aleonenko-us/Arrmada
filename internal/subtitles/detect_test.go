package subtitles

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// Captured whisper-cli -dl output (whisper.cpp 1.9): the detection line sits among the
// model-load chatter on stderr.
const detectOutput = `whisper_init_from_file_with_params_no_state: loading model from '/data/whisper/ggml-large-v3-turbo.bin'
whisper_model_load: n_vocab       = 51866
whisper_backend_init_gpu: using SYCL0 backend
system_info: n_threads = 16 / 24 | WHISPER : COREML = 0 | OPENVINO = 0 |
main: processing '/tmp/whisper-dl-1.wav' (480000 samples, 30.0 sec), 16 threads, 1 processors, 5 beams + best of 5, lang = auto, task = transcribe, timestamps = 1 ...
whisper_full_with_state: auto-detected language: en (p = 0.970215)
whisper_print_timings:     load time =   812.33 ms
`

func TestParseDetectedLanguage(t *testing.T) {
	d, ok := parseDetectedLanguage([]byte(detectOutput))
	if !ok || d.Lang != "en" || d.Prob < 0.97 || d.Prob > 0.971 {
		t.Errorf("got %+v ok=%v", d, ok)
	}
	for out, want := range map[string]DetectedLang{
		"whisper_full_with_state: auto-detected language: fr (p = 0.512000)": {"fr", 0.512},
		"auto-detected language: ja (p = 1.000000)":                          {"ja", 1},
		"auto-detected language: haw (p = 0.700000)":                         {"haw", 0.7},
	} {
		if got, ok := parseDetectedLanguage([]byte(out)); !ok || got != want {
			t.Errorf("%q → %+v ok=%v, want %+v", out, got, ok, want)
		}
	}
	if _, ok := parseDetectedLanguage([]byte("whisper_print_timings: load time = 812 ms")); ok {
		t.Error("found a language in output without one")
	}
}

// Detection runs whisper-cli with -dl -l auto and nothing that 1.9 builds mishandle.
func TestDetectArgs(t *testing.T) {
	args := detectArgs("/m/ggml-large-v3-turbo.bin", "/tmp/s.wav")
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-dl -l auto") || !strings.HasPrefix(got, "-m /m/ggml-large-v3-turbo.bin -f /tmp/s.wav") {
		t.Errorf("args %q", got)
	}
	for _, banned := range []string{"-ml", "-sow", "--vad", "--translate"} {
		if slices.Contains(args, banned) {
			t.Errorf("args carry %s: %q", banned, got)
		}
	}
}

// The sample is the first speech chunk past 10% of the runtime, at most 30 s of it.
func TestDetectionWindow(t *testing.T) {
	m := time.Minute
	chunks := []chunk{{0, 4 * m}, {4*m + 5*time.Second, 9 * m}, {12 * m, 12*m + 10*time.Second}}
	if w := detectionWindow(40*m, chunks); w.start != 4*m+5*time.Second || w.end != 4*m+35*time.Second {
		t.Errorf("window %v-%v", w.start, w.end)
	}
	if w := detectionWindow(100*m, chunks[:2]); w.start != 10*m || w.end != 10*m+30*time.Second {
		t.Errorf("no chunk past 10%%: window %v-%v", w.start, w.end)
	}
	if w := detectionWindow(110*m, []chunk{{12 * m, 12*m + 10*time.Second}}); w.end != 12*m+10*time.Second {
		t.Errorf("short chunk: window %v-%v", w.start, w.end)
	}
}

func untaggedAudio() *mediaInfo {
	return &mediaInfo{Audio: []AudioTrack{{Index: 0, Lang: "und"}}}
}

func TestAIPlanUsesDetectedLanguage(t *testing.T) {
	mi := untaggedAudio()
	if aiPlan(mi.withDetected().AudioLangs, "en") != "translate" {
		t.Error("undetected untagged audio no longer falls back to translate (-l auto)")
	}
	mi.Detected = map[int]DetectedLang{0: {"en", 0.95}}
	heard := mi.withDetected()
	if aiPlan(heard.AudioLangs, "en") != "transcribe" {
		t.Errorf("detected English: %v", heard.AudioLangs)
	}
	if st, tr := audioStreamFor(heard, "en", false); st != 0 || tr.Lang != "en" {
		t.Errorf("stream %d %+v", st, tr)
	}
	if mi.Audio[0].Lang != "und" || len(mi.AudioLangs) != 0 {
		t.Errorf("withDetected changed the probe: %+v", mi)
	}
	mi.Detected = map[int]DetectedLang{0: {"fr", 0.9}}
	heard = mi.withDetected()
	if aiPlan(heard.AudioLangs, "en") != "translate" || aiPlan(heard.AudioLangs, "fr") != "transcribe" || aiPlan(heard.AudioLangs, "es") != "" {
		t.Errorf("detected French: %v", heard.AudioLangs)
	}
	if _, tr := audioStreamFor(heard, "en", true); tr.Lang != "fr" {
		t.Errorf("translate source %+v, want fr", tr)
	}
	mi.Detected = map[int]DetectedLang{0: {"en", 0.4}} // not sure enough
	if aiPlan(mi.withDetected().AudioLangs, "en") != "translate" {
		t.Error("an unsure detection was used")
	}
	tagged := &mediaInfo{AudioLangs: []string{"eng"}, Audio: []AudioTrack{{Index: 0, Lang: "eng"}}, Detected: map[int]DetectedLang{0: {"fr", 1}}}
	if got := tagged.withDetected().AudioLangs; len(got) != 1 || got[0] != "eng" {
		t.Errorf("a detection overrode a tag: %v", got)
	}
}

// detectingAI is fakeAI plus language detection, counting how often it listens.
type detectingAI struct {
	*fakeAI
	heard   DetectedLang
	detects int
}

func (a *detectingAI) detect(context.Context, string, string) (DetectedLang, error) {
	a.detects++
	return a.heard, nil
}

// detectFixture is a one-language movie whose audio extraction writes a small WAV.
func detectFixture(t *testing.T, want string, mi *mediaInfo, ai aiRunner) (*Service, string, *int) {
	t.Helper()
	s, video := ladderFixture(t, want, mi, nil, ai)
	extracts := 0
	s.extractAudio = func(_ context.Context, _ string, _ int, dst string) error {
		extracts++
		return os.WriteFile(dst, make([]byte, 8192), 0o644)
	}
	return s, video, &extracts
}

// An untagged-English WEB-DL on a turbo-only install is transcribed with turbo.
func TestUntaggedEnglishTranscribedWithTurbo(t *testing.T) {
	ai := &detectingAI{fakeAI: &fakeAI{avail: true, transcribe: true}, heard: DetectedLang{"en", 0.97}}
	s, video, extracts := detectFixture(t, "en", untaggedAudio(), ai)
	job := runJob(s)
	if job.State != StateDone || ai.detects != 1 || strings.Join(ai.calls, ",") != "en/transcribe" {
		t.Fatalf("state %q note %q detects %d calls %v", job.State, job.Note, ai.detects, ai.calls)
	}
	if *extracts != 1 {
		t.Errorf("audio extracted %d times for detection + whisper", *extracts)
	}
	assertSidecar(t, video, "en")
}

// The detected language is kept with the probe: a second job on the same file doesn't
// listen again.
func TestDetectionCachedInProbeCache(t *testing.T) {
	ai := &detectingAI{fakeAI: &fakeAI{avail: true, transcribe: true}, heard: DetectedLang{"en", 0.97}}
	s, video, _ := detectFixture(t, "en", nil, ai)
	s.probe = nil
	s.cache = &probeCache{db: s.ledger.db}
	probes := 0
	s.ffprobeRun = func(context.Context, string, string) (*mediaInfo, error) {
		probes++
		mi := untaggedAudio()
		mi.ProbeVersion = probeVersion
		return mi, nil
	}
	if job := runJob(s); job.State != StateDone {
		t.Fatalf("first job %q %q", job.State, job.Note)
	}
	_ = os.Remove(sidecarPath(video, "en"))
	if job := runJob(s); job.State != StateDone {
		t.Fatalf("second job %q %q", job.State, job.Note)
	}
	if ai.detects != 1 || probes != 1 {
		t.Errorf("detects %d probes %d, want one of each", ai.detects, probes)
	}
	mi, err := s.probeCached(context.Background(), video)
	if err != nil || mi.Detected[0].Lang != "en" {
		t.Errorf("cached probe %+v err %v", mi, err)
	}
}

// Tagged audio never sends whisper off to listen.
func TestTaggedAudioSkipsDetection(t *testing.T) {
	ai := &detectingAI{fakeAI: &fakeAI{avail: true, transcribe: true}, heard: DetectedLang{"fr", 1}}
	s, _, _ := detectFixture(t, "en", englishAudio(), ai)
	if job := runJob(s); job.State != StateDone || ai.detects != 0 {
		t.Errorf("state %q detects %d", job.State, ai.detects)
	}
	// A wanted language the tags don't cover doesn't make a tagged track worth a listen.
	mi := &mediaInfo{AudioLangs: []string{"fre"}, Audio: []AudioTrack{{Index: 0, Lang: "fre"}}}
	s, _, _ = detectFixture(t, "en", mi, ai)
	runJob(s)
	if ai.detects != 0 {
		t.Errorf("a tagged track was detected (%d)", ai.detects)
	}
}

// French audio on a turbo-only install waits for large-v3: model_missing, parked until a
// model download, counted on the Settings card.
func TestAIPlanModelMissingOutcome(t *testing.T) {
	ai := &detectingAI{fakeAI: &fakeAI{avail: true, transcribe: true}, heard: DetectedLang{"fr", 0.92}}
	s, _, _ := detectFixture(t, "en", untaggedAudio(), ai)
	job := runJob(s)
	if len(job.Outcomes) != 1 || job.Outcomes[0].Result != ResultModelMissing ||
		job.Outcomes[0].Detail != "needs large-v3 to translate fr audio to English" || len(ai.calls) != 0 {
		t.Fatalf("outcomes %+v calls %v", job.Outcomes, ai.calls)
	}
	ctx := context.Background()
	bs, _ := s.ledger.BackoffsFor(ctx, "m:1")
	b, ok := bs[BackoffKey("m:1", "en", "")]
	if !ok || b.PrimaryReason != ResultModelMissing || b.NextRetryAt != 0 || strings.Join(b.ClearOn, ",") != EventModel {
		t.Errorf("backoff %+v ok=%v", b, ok)
	}
	if st := s.WhisperStatus(ctx); st.Waiting != 1 {
		t.Errorf("waiting = %d", st.Waiting)
	}
	s.ClearBackoff(ctx, EventModel)
	if st := s.WhisperStatus(ctx); st.Waiting != 0 {
		t.Errorf("waiting after a model download = %d", st.Waiting)
	}
}

// The Library offers the AI fallback for untagged audio nobody has listened to yet on a
// turbo-only install: detection may well find it's the wanted language.
func TestFillCoverageUnknownAudio(t *testing.T) {
	s, video := ladderFixture(t, "en", untaggedAudio(), &fakeProvider{canDownload: true}, &fakeAI{avail: true, transcribe: true})
	fs := FileSubs{Kind: "movie", Path: video}
	s.fillCoverage(context.Background(), &fs, []string{"en"}, true)
	if fs.Languages[0].Fallback != "ai" {
		t.Errorf("languages %+v", fs.Languages)
	}
}
