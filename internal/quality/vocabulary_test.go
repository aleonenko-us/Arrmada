package quality

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
)

func vocabHas(vs []VocabValue, v string) bool {
	for _, x := range vs {
		if x.Value == v {
			return true
		}
	}
	return false
}

// The editor's dropdowns offer everything the parser can produce, so no value a release
// can carry is missing from them.
func TestConditionVocabularyCoversTheParser(t *testing.T) {
	vocab := ConditionVocabulary()
	for _, s := range []parser.Source{parser.SourceRemux, parser.SourceBluray, parser.SourceWebDL, parser.SourceWebRip,
		parser.SourceHDTV, parser.SourceDVD, parser.SourceCAM} {
		if !vocabHas(vocab[CondSource], string(s)) {
			t.Errorf("source %q missing", s)
		}
	}
	for _, c := range []parser.Codec{parser.CodecX265, parser.CodecX264, parser.CodecXvid, parser.CodecVC1, parser.CodecAV1} {
		if !vocabHas(vocab[CondCodec], string(c)) {
			t.Errorf("codec %q missing", c)
		}
	}
	for _, r := range []parser.Resolution{parser.Res2160p, parser.Res1080p, parser.Res720p, parser.Res576p, parser.Res480p} {
		if !vocabHas(vocab[CondResolution], string(r)) {
			t.Errorf("resolution %q missing", r)
		}
	}
	for _, a := range parser.AudioLabels() {
		if !vocabHas(vocab[CondAudio], a) {
			t.Errorf("audio %q missing", a)
		}
	}
	for _, e := range parser.Editions() {
		if !vocabHas(vocab[CondEdition], e) {
			t.Errorf("edition %q missing", e)
		}
	}
	// Every value offered is one the engine reads as itself: picking it from the dropdown
	// stores something that matches.
	for typ, vs := range vocab {
		for _, v := range vs {
			if got := normalizeValue(typ, v.Value); got != v.Value {
				t.Errorf("%s %q normalises to %q", typ, v.Value, got)
			}
			if v.Label == "" {
				t.Errorf("%s %q has no label", typ, v.Value)
			}
		}
	}
}

func TestCheckFormats(t *testing.T) {
	fs := []CustomFormat{
		{Name: "WEB 4K", Conditions: []Condition{{Type: CondSource, Value: "WEB"}, {Type: CondResolution, Value: "4K"}}},
		{Name: "Broken", Conditions: []Condition{{Type: CondReleaseTitle, Value: "(oops"}}},
		{Name: "Never", Conditions: []Condition{{Type: CondCodec, Value: "h266"}}},
	}
	got := CheckFormats(fs)
	if got.Samples != len(SampleCandidates()) || len(got.Matches) != 3 {
		t.Fatalf("check = %+v", got)
	}
	if got.Matches[0] != 2 { // the two 2160p WEB-DL samples
		t.Errorf("WEB 4K matched %d samples, want 2", got.Matches[0])
	}
	if got.Matches[1] != 0 || got.Matches[2] != 0 {
		t.Errorf("broken/never matched: %v", got.Matches)
	}
	if len(got.Errors) != 1 || got.Errors[0].Index != 1 || got.Errors[0].Condition != 0 {
		t.Errorf("errors = %+v", got.Errors)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Index != 2 {
		t.Errorf("warnings = %+v", got.Warnings)
	}
	if empty := CheckFormats(nil); empty.Errors == nil || empty.Warnings == nil {
		t.Error("an empty check should answer empty lists, not null")
	}
}
