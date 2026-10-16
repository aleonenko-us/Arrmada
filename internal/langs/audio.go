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

// NewAudioLang is the badge for a tagged (or detected) language: its base code and name.
// An empty or unknown tag gives the unidentified badge (Code "").
func NewAudioLang(tag string, detected bool) AudioLang {
	l, ok := Lookup(tag)
	if !ok {
		return AudioLang{}
	}
	base, _ := Lookup(l.ISO1)
	return AudioLang{Code: l.ISO1, Name: base.Name, Detected: detected}
}

// OrderAudio is the badges a file shows: one per language — a tagged track beats a
// detected one of the same language — the title's original language first, then the rest
// by code, and a single "not identified" badge last.
func OrderAudio(list []AudioLang, orig string) []AudioLang {
	o := ""
	if l, ok := Lookup(orig); ok {
		o = l.ISO1
	}
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
