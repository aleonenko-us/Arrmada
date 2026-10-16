package parser

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// The release traits behind the quality engine's Unwanted pack and its custom formats:
// a full disc image, 3D, an upscale, a bonus-material release, the audio languages a name
// states, and the streaming service it was captured from.
//
// Every one of them is read only from the tail of the name — what follows the title cut
// (the year, or the season/episode marker) — so a word in a title is never taken for a
// tag: "The 3D Man", "Extras" and "French Kiss" are titles, not traits. Within a TV
// episode's tail the episode title sits between the marker and the first technical token;
// there only a language written the scene way (FRENCH, iTALiAN) counts, and nothing else.
//
// Detection is deliberately conservative and token-bounded: these traits reject or avoid
// releases, and a false positive costs a good grab.

// tagToken is one word of a release name's tail.
type tagToken struct {
	raw string // as written
	lc  string // lower case
	// title is set for a word inside a TV episode title (between the episode marker and
	// the first technical token): only scene-cased language words count there.
	title bool
}

// tailTokens splits the part of name after the title cut into words. cut is where the
// title ends (len(name) when no year or marker ended it, in which case the tail starts at
// the first technical token, if any). group is the trailing release group, left out so a
// group named "NF" or "3D" isn't read as a tag. episodeTitle says the release names
// episodes, so an episode title may follow the marker.
func tailTokens(name string, cut int, group string, episodeTitle bool) []tagToken {
	start := cut
	if start >= len(name) {
		q := reQualityStart.FindStringIndex(name)
		if q == nil || q[0] == 0 {
			return nil
		}
		start = q[0]
	}
	tail := name[start:]
	if group != "" && strings.HasSuffix(tail, "-"+group) {
		tail = strings.TrimSuffix(tail, "-"+group)
	}
	titleEnd := 0
	if episodeTitle {
		titleEnd = len(tail)
		if q := reEpisodeQualityStart.FindStringIndex(tail); q != nil {
			titleEnd = q[0]
		}
	}
	var out []tagToken
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; i < len(tail); {
		if r, size := utf8.DecodeRuneInString(tail[i:]); !word(r) {
			i += size
			continue
		}
		j := i
		for j < len(tail) {
			r, size := utf8.DecodeRuneInString(tail[j:])
			if !word(r) {
				break
			}
			j += size
		}
		out = append(out, tagToken{raw: tail[i:j], lc: strings.ToLower(tail[i:j]), title: i < titleEnd})
		i = j
	}
	return out
}

// sceneCased reports a word written the way scene tags are: all capitals, or capitals with
// the lower-case "i" groups use ("MULTi", "iTALiAN"). "French" in an episode title isn't.
func sceneCased(raw string) bool {
	hasUpper := false
	for _, r := range raw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r) && r != 'i':
			return false
		}
	}
	return hasUpper
}

// tags is the tail's words with the lookups the detectors share.
type tags struct {
	toks []tagToken // only the words outside an episode title
	all  []tagToken // every word, for languages
	has  map[string]bool
}

func newTags(all []tagToken) tags {
	t := tags{all: all, has: map[string]bool{}}
	for _, tok := range all {
		if !tok.title {
			t.toks = append(t.toks, tok)
			t.has[tok.lc] = true
		}
	}
	return t
}

// any reports whether any of words is a tag.
func (t tags) any(words ...string) bool {
	for _, w := range words {
		if t.has[w] {
			return true
		}
	}
	return false
}

// seq reports whether words appear as consecutive tags ("full", "blu", "ray").
func (t tags) seq(words ...string) bool {
	for i := 0; i+len(words) <= len(t.toks); i++ {
		ok := true
		for k, w := range words {
			if t.toks[i+k].lc != w {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// detectTraits fills FullDisc, ThreeD, Upscaled, Extras, Languages, Multi and Streaming
// from the tail's words. fansub marks a fansub-style name, whose BD encodes name their
// codec without an encoder tag and so are never inferred to be discs.
func (r *Release) detectTraits(all []tagToken, fansub bool) {
	t := newTags(all)
	r.Languages, r.Multi = detectLanguages(t)
	r.Streaming = detectStreaming(t)
	r.FullDisc = detectFullDisc(t, r.Languages, fansub)
	r.ThreeD = t.any("3d", "sbs", "hsbs", "hou") || t.seq("h", "sbs") || t.seq("half", "sbs") ||
		t.seq("half", "ou") || t.seq("h", "ou")
	// "Regrade" is a colour pass, not an upscale, and isn't listed.
	r.Upscaled = t.any("upscaled", "upscale", "upscaling")
	r.Extras = t.any("extras", "featurette", "featurettes", "bonus") || t.seq("special", "features") ||
		t.seq("behind", "the", "scenes")
}

// detectFullDisc reports an untouched disc image (BDMV folder or ISO), which the importer
// can't import. Explicit disc tokens always count; otherwise a Blu-ray or HD DVD source
// with the disc's own video codec (AVC, HEVC, VC-1, MVC, MPEG-2) and no sign of an encode
// or remux does, the way TRaSH's BR-DISK format reads it (RE2 has no lookaround, hence Go).
func detectFullDisc(t tags, languages []string, fansub bool) bool {
	if t.any("bdmv", "bdiso", "brdisk", "bd25", "bd50", "bd66", "bd100") ||
		t.seq("br", "disk") || t.seq("3d", "bd") || t.seq("bd", "iso") || t.seq("uhd", "iso") ||
		t.seq("full", "bluray") || t.seq("full", "blu", "ray") {
		return true
	}
	encoded := t.any("remux", "bdremux", "bdrip", "brrip", "x264", "x265", "h264", "h265", "720p",
		"xvid", "divx", "mkv", "dvdrip", "webrip", "webdl", "web") || t.seq("h", "264") || t.seq("h", "265")
	if encoded {
		return false
	}
	if t.seq("complete", "bluray") || t.seq("complete", "uhd", "bluray") ||
		t.seq("complete", "blu", "ray") || t.seq("complete", "uhd", "blu", "ray") {
		return true
	}
	// German groups label their remuxes "German DL 1080p BluRay AVC"; TRaSH excludes them
	// for the same reason, and fansub BD encodes name only the codec.
	if fansub || containsStr(languages, "de") {
		return false
	}
	disc := t.any("bluray", "bd", "hddvd") || t.seq("blu", "ray") || t.seq("hd", "dvd")
	discCodec := t.any("avc", "mvc", "vc1", "mpeg2", "iso") || t.seq("vc", "1") || t.seq("mpeg", "2")
	// A 1080p HEVC release is an encode: 1080p Blu-rays are AVC (or VC-1/MPEG-2), so HEVC
	// at 1080p came from somebody's encoder. Only UHD discs are HEVC.
	if !discCodec && t.has["hevc"] && !t.has["1080p"] && !t.has["1080i"] {
		discCodec = true
	}
	return disc && discCodec
}

// sceneLanguages are the language tags release names use that aren't language names:
// French dub variants, the Spanish regional words and the Polish dub tags. Mapped to codes
// the language table knows; everything else is read through it.
var sceneLanguages = map[string]string{
	"truefrench": "fr", "vff": "fr", "vfq": "fr", "vf2": "fr", "vfi": "fr",
	"castellano": "es", "pldub": "pl", "dubpl": "pl",
}

// languageCodes3 are the three-letter codes release names commonly carry as audio tags
// ("ITA.ENG", "POL.ENG"). Only these, and only in capitals: most three-letter codes are
// words too, which is why the ones that are everyday tags or words ("NOR", "FIN", "DAN")
// stay out.
var languageCodes3 = map[string]bool{
	"ita": true, "eng": true, "ger": true, "fre": true, "spa": true, "jpn": true, "kor": true, "rus": true,
	"pol": true, "por": true, "dut": true, "cze": true, "hun": true, "tur": true, "ukr": true,
	"chi": true, "hin": true, "swe": true, "heb": true,
}

// subsWords after a language say it names the subtitles, not the audio ("German.Subbed",
// "ENG.SUBS", "VOSTFR" is one word and never read).
var subsWords = map[string]bool{
	"sub": true, "subs": true, "subbed": true, "subtitle": true, "subtitles": true, "subtitled": true,
	"hardsub": true, "hardsubs": true, "hardsubbed": true, "softsub": true, "softsubs": true,
}

// detectLanguages reads the audio languages a name states, as ISO 639-1 codes in the order
// written, and whether it says it carries several (MULTi, DUAL, or German "DL"/"ML" next
// to a language). Language names are looked up in the one language table (internal/langs).
func detectLanguages(t tags) ([]string, bool) {
	var out []string
	multi := false
	words := t.all
	for i, tok := range words {
		next := ""
		if i+1 < len(words) {
			next = words[i+1].lc
		}
		// German TV names its dubs "Show.S01E01.German.DL.1080p…", with the language where
		// an episode title would sit; "DL"/"ML" right after it says it's the tag.
		if tok.title && !sceneCased(tok.raw) && next != "dl" && next != "ml" {
			continue
		}
		if tok.lc == "multi" || tok.lc == "dual" {
			if !subsWords[next] { // "Multi-Subs" is subtitles
				multi = true
			}
			continue
		}
		code := ""
		switch {
		case sceneLanguages[tok.lc] != "":
			code = langs.Base(sceneLanguages[tok.lc])
		case languageCodes3[tok.lc]:
			if sceneCased(tok.raw) {
				code = langs.ISO1(tok.lc)
			}
		case len(tok.lc) >= 4:
			if l, ok := langs.Lookup(tok.lc); ok && langs.IsToken(tok.lc, true) {
				code = l.ISO1
			}
		}
		if code == "" || subsWords[next] {
			continue
		}
		if !containsStr(out, code) {
			out = append(out, code)
		}
	}
	if !multi && len(out) > 0 {
		// German "DL" (dual language) / "ML" (multi language) — but "WEB-DL" is a source.
		// Read over every word: in an episode name the "German.DL" sits where a title would.
		for i, tok := range words {
			if (tok.lc == "dl" || tok.lc == "ml") && (i == 0 || words[i-1].lc != "web") {
				multi = true
				break
			}
		}
	}
	return out, multi
}

// streamingServices are the services a WEB release names, by the tag as written. long ones
// are distinctive enough in any case; short ones must be in capitals, since "Stan" or "Max"
// can be words; MA and iT also need a WEB tag beside them ("DTS-HD.MA" is audio).
var streamingServices = []struct {
	tag     string
	anyCase bool
	nearWeb bool
}{
	{"AMZN", true, false}, {"NF", false, false}, {"ATVP", true, false}, {"DSNP", true, false},
	{"HMAX", true, false}, {"MAX", false, false}, {"PCOK", true, false}, {"PMTP", true, false},
	{"HULU", true, false}, {"iT", false, true}, {"CRAV", true, false}, {"STAN", false, false},
	{"MA", false, true},
}

// detectStreaming returns the first streaming service the tail names, "" for none.
func detectStreaming(t tags) string {
	for i, tok := range t.toks {
		for _, s := range streamingServices {
			if s.anyCase {
				if tok.lc != strings.ToLower(s.tag) {
					continue
				}
			} else if tok.raw != s.tag {
				continue
			}
			if s.nearWeb && !besideWeb(t.toks, i) {
				continue
			}
			return s.tag
		}
	}
	return ""
}

// besideWeb reports whether the word at i has a WEB source tag next to it: "MA.WEB-DL",
// "WEB-DL.MA", "iT.WEBRip".
func besideWeb(toks []tagToken, i int) bool {
	isWeb := func(k int) bool {
		if k < 0 || k >= len(toks) {
			return false
		}
		switch toks[k].lc {
		case "web", "webdl", "webrip":
			return true
		case "dl", "rip":
			return k > 0 && toks[k-1].lc == "web"
		}
		return false
	}
	return isWeb(i-1) || isWeb(i+1)
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
