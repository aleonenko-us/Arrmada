package subtitles

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// langArgs is the "-l …"/"--translate" part of a whisper-cli command line.
func langArgs(args []string) string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-l":
			out = append(out, "-l", args[i+1])
			i++
		case "--translate":
			out = append(out, "--translate")
		}
	}
	return strings.Join(out, " ")
}

// A film whose audio is tagged French is translated as French, not decoded under
// whisper-cli's default English token.
func TestArgsTranslatePassesSourceLang(t *testing.T) {
	w := &whisperGen{}
	for src, want := range map[string]string{
		"fre": "-l fr --translate", "fra": "-l fr --translate", "ger": "-l de --translate",
		"jpn": "-l ja --translate", "pt-BR": "-l pt --translate", "chi": "-l zh --translate",
	} {
		if got := langArgs(w.args("m.bin", "a.wav", "out", "en", src, true, false)); got != want {
			t.Errorf("audio %s: %q, want %q", src, got, want)
		}
	}
}

// Untagged (or "und", or nonsense-tagged) audio lets whisper detect the language.
func TestArgsTranslateUnknownUsesAuto(t *testing.T) {
	w := &whisperGen{}
	for _, src := range []string{"", "und", "xx"} {
		if got := langArgs(w.args("m.bin", "a.wav", "out", "en", src, true, false)); got != "-l auto --translate" {
			t.Errorf("audio %q: %q, want -l auto --translate", src, got)
		}
	}
}

// Transcribe runs keep exactly the arguments they always had.
func TestArgsTranscribeUnchanged(t *testing.T) {
	w := &whisperGen{noFallback: true, suppressNST: true, flashAttn: true, noContext: true, carryPrompt: true}
	got := w.args("/m/ggml-large-v3-turbo.bin", "a.wav", "out", "en", "eng", false, true)
	want := []string{"-m", "/m/ggml-large-v3-turbo.bin", "-f", "a.wav", "-ojf", "-of", "out", "-t", strconv.Itoa(threads()), "-pp",
		"-l", "en"}
	if p := dtwPreset("/m/ggml-large-v3-turbo.bin"); p != "" {
		want = append(want, "-dtw", p)
	}
	want = append(want, "-nf", "--suppress-nst", "-fa", "-nc", "--prompt", stylePromptEnglish, "--carry-initial-prompt")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("transcribe args:\n got %q\nwant %q", got, want)
	}
	// The source language never leaks into a transcription.
	if got := langArgs(w.args("m.bin", "a.wav", "out", "fr", "jpn", false, false)); got != "-l fr" {
		t.Errorf("transcribe fr: %q", got)
	}
	// A regional want is spoken as its base language.
	if got := langArgs(w.args("m.bin", "a.wav", "out", "pt-br", "", false, false)); got != "-l pt" {
		t.Errorf("transcribe pt-br: %q", got)
	}
}

// whisper's -l takes the two-letter code, whichever spelling the track was tagged with.
func TestISO1Mapping(t *testing.T) {
	for in, want := range map[string]string{
		"eng": "en", "en": "en", "english": "en", "fre": "fr", "fra": "fr", "French": "fr",
		"ger": "de", "deu": "de", "chi": "zh", "zho": "zh", "dut": "nl", "nld": "nl",
		"pt-br": "pt", "zh-tw": "zh", "und": "", "": "", "xx": "",
	} {
		if got := langs.ISO1(in); got != want {
			t.Errorf("ISO1(%q) = %q, want %q", in, got, want)
		}
	}
}

// The ladder hands whisper the chosen audio track's language when it translates.
func TestProcessTranslatePassesAudioLang(t *testing.T) {
	ai := &fakeAI{avail: true, transcribe: true, translate: true}
	mi := &mediaInfo{AudioLangs: []string{"fre"}, Audio: []AudioTrack{{Index: 0, Lang: "fre", Default: true}}}
	s, video := ladderFixture(t, "en", mi, nil, ai)
	runJob(s)
	assertSidecar(t, video, "en")
	if !reflect.DeepEqual(ai.calls, []string{"en/translate"}) || !reflect.DeepEqual(ai.srcs, []string{"fre"}) {
		t.Errorf("calls=%v srcs=%v, want one translate from fre", ai.calls, ai.srcs)
	}
}
