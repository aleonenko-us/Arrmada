package langs

import (
	"reflect"
	"testing"
)

// Every spelling a muxer writes folds to the one base code and its English name: 639-2/B
// and /T alike, names, regional tags.
func TestNewAudioLangNormalises(t *testing.T) {
	cases := map[string]string{
		"ger": "de", "deu": "de", "German": "de", "fre": "fr", "fra": "fr", "eng": "en",
		"en-US": "en", "pt-BR": "pt", "pob": "pt", "spa": "es", "es-419": "es", "jpn": "ja",
		"chi": "zh", "zho": "zh", "dut": "nl", "nld": "nl", "iw": "he",
	}
	for tag, want := range cases {
		got := NewAudioLang(tag, false)
		if got.Code != want || got.Name == "" {
			t.Errorf("NewAudioLang(%q) = %+v, want code %q with a name", tag, got, want)
		}
	}
	// Norwegian's written forms are one language to a listener.
	for _, tag := range []string{"nb", "nob", "nn", "nno", "no", "nor", "Norwegian Bokmål"} {
		if got := NewAudioLang(tag, false); got.Code != "no" || got.Name != "Norwegian" {
			t.Errorf("NewAudioLang(%q) = %+v, want Norwegian (no)", tag, got)
		}
		if AudioBase(tag) != "no" {
			t.Errorf("AudioBase(%q) = %q", tag, AudioBase(tag))
		}
	}
	// TMDB's Cantonese codes ("cn" for most Hong Kong films, "yue") are spoken Chinese;
	// "xx" is TMDB's "No Language": nothing.
	for _, tag := range []string{"cn", "CN", "yue"} {
		if AudioBase(tag) != "zh" || NewAudioLang(tag, false).Name != "Chinese" {
			t.Errorf("AudioBase(%q) = %q", tag, AudioBase(tag))
		}
	}
	if AudioBase(NoLanguage) != "" {
		t.Errorf("AudioBase(xx) = %q, want none", AudioBase(NoLanguage))
	}
	if got := NewAudioLang("ger", false); got.Name != "German" {
		t.Errorf("name = %q, want German", got.Name)
	}
	if got := NewAudioLang("pt-br", false); got.Name != "Portuguese" {
		t.Errorf("a regional tag badges its base language: %q", got.Name)
	}
	for _, tag := range []string{"", "und", "zxx", "qaa-nonsense", "xx"} {
		if got := NewAudioLang(tag, false); got.Code != "" {
			t.Errorf("NewAudioLang(%q) = %+v, want the unidentified badge", tag, got)
		}
	}
}

// Original language first, then the rest by code, deduplicated (a tag beats a detection of
// the same language), and one "not identified" badge last however many untagged tracks.
func TestOrderAudio(t *testing.T) {
	de, en, fr := NewAudioLang("de", false), NewAudioLang("en", false), NewAudioLang("fr", false)
	enHeard := NewAudioLang("en", true)
	got := OrderAudio([]AudioLang{fr, {}, enHeard, de, {}, en, de}, "eng")
	want := []AudioLang{en, de, fr, {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderAudio = %+v\nwant %+v", got, want)
	}
	// An unknown original language: alphabetical.
	if got := OrderAudio([]AudioLang{fr, de}, ""); got[0].Code != "de" || got[1].Code != "fr" {
		t.Errorf("no original: %+v", got)
	}
	// Only detected: kept, marked.
	if got := OrderAudio([]AudioLang{enHeard}, "en"); len(got) != 1 || !got[0].Detected {
		t.Errorf("detected only: %+v", got)
	}
	if got := OrderAudio(nil, "en"); len(got) != 0 {
		t.Errorf("nothing: %+v", got)
	}
}
