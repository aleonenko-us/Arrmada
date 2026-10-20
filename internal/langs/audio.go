package langs

import "sort"

// AudioLang is one audio language a library file carries, as the movie and series pages
// badge it. The file's own track tags are read first; an untagged track takes the language
// the subtitle AI heard on it, marked Detected; one nothing identified is Code "".
type AudioLang struct {
	Code     string `json:"code"`               // base ISO 639-1 code ("de"); "" = an untagged track nobody identified
	Name     string `json:"name,omitempty"`     // English name, for the tooltip and screen readers
	Detected bool   `json:"detected,omitempty"` // heard by the AI on an untagged track, not tagged in the file
}

// Audio-fit statuses: how a file's languages meet its profile's language rules.
const (
	AudioFitGood    = "good"    // every Must, and a Prefer when the profile has any
	AudioFitPartial = "partial" // every Must, but none of the preferred languages
	AudioFitBad     = "bad"     // a Must is missing, or every language it has is avoided
)

// AudioFit is a file's audio languages judged against its profile's language rules. Note
// says it in words, so the colour is never the only signal.
type AudioFit struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// audioFold folds the written forms of one spoken language together for audio: Bokmål
// and Nynorsk are both Norwegian to a listener, and release names and track tags use
// all three (nb/nob, nn/nno, no/nor) for the same dub.
var audioFold = map[string]string{"nb": "no", "nn": "no"}

// audioAlias are codes outside the table that name a language it knows. TMDB codes
// Cantonese — most Hong Kong films — as "cn" (its own code) or "yue" (ISO 639-3), and
// tracks are sometimes tagged "yue": spoken Chinese, which release names call CHINESE or
// CHI, so all of them are "zh".
var audioAlias = map[string]string{"cn": "zh", "yue": "zh"}

// NoLanguage is TMDB's original_language for a title with no spoken language (silent
// films, music, nature footage). AudioBase gives "" for it: no original language to read
// an untagged release as, so the audio rules can't judge one and refuse nothing.
const NoLanguage = "xx"

// AudioBase is the base code an audio track or release language is compared and badged
// by: ISO1, with Norwegian's written forms folded to "no" and TMDB's Cantonese codes to
// "zh" (audioAlias). "" when the tag isn't known, and for NoLanguage.
func AudioBase(tag string) string {
	n := norm(tag)
	if n == NoLanguage {
		return ""
	}
	if a, ok := audioAlias[n]; ok {
		return a
	}
	c := ISO1(tag)
	if f, ok := audioFold[c]; ok {
		return f
	}
	return c
}

// NewAudioLang is the badge for a tagged (or detected) language: its base code and name.
// An empty or unknown tag gives the unidentified badge (Code "").
func NewAudioLang(tag string, detected bool) AudioLang {
	code := AudioBase(tag)
	if code == "" {
		return AudioLang{}
	}
	base, _ := Lookup(code)
	return AudioLang{Code: code, Name: base.Name, Detected: detected}
}

// OrderAudio is the badges a file shows: one per language — a tagged track beats a
// detected one of the same language — the title's original language first, then the rest
// by code, and a single "not identified" badge last.
func OrderAudio(list []AudioLang, orig string) []AudioLang {
	o := AudioBase(orig)
	byCode := map[string]int{}
	var out []AudioLang
	unknown := false
	for _, a := range list {
		if a.Code == "" {
			unknown = true
			continue
		}
		if i, ok := byCode[a.Code]; ok {
			if out[i].Detected && !a.Detected {
				out[i] = a
			}
			continue
		}
		byCode[a.Code] = len(out)
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Code == o) != (out[j].Code == o) {
			return out[i].Code == o
		}
		return out[i].Code < out[j].Code
	})
	if unknown {
		out = append(out, AudioLang{})
	}
	return out
}
