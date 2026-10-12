// Package langs is Arrmada's one language table: every ISO 639-1 language with its
// 639-2/B and /T codes and English name, plus the regional variants people actually ask
// subtitles for (pt-br, zh-tw, es-419…). Subtitles, the importer and Convert all read
// language tags through it, so "fre", "fra", "fr" and "French" are the same language
// everywhere and an unknown tag is never mistaken for one.
//
// It is a leaf package (standard library only). The table itself is generated from the
// vendored gen/iso639.tsv into table_gen.go; edit the TSV and run go generate.
//
// Codes come in many spellings; the canonical form is the 639-1 code, lower case ("fr"),
// or a regional tag "<639-1>-<region>" ("pt-br"). Every function accepts any spelling.
package langs

//go:generate go run ./gen

import (
	"strings"
)

// Language is one entry of the table.
type Language struct {
	Code     string `json:"code"`     // canonical: "fr", "pt-br"
	Name     string `json:"name"`     // English name: "French", "Portuguese (Brazil)"
	ISO1     string `json:"-"`        // 639-1 base language: "pt" for pt-br
	ISO2B    string `json:"-"`        // 639-2/B ("fre"), the base's for a regional variant
	ISO2T    string `json:"-"`        // 639-2/T ("fra"), likewise
	Regional bool   `json:"regional"` // a regional variant of ISO1

	aliases  []string // other spellings of exactly this entry ("brazilian", "pob", "iw")
	openSubs string   // OpenSubtitles.com code when it differs from Code
}

// Subtitle variants, as sidecar names spell them. A forced track carries only the
// foreign-language lines (signs, a line of Klingon) and is never the full subtitle; SDH
// is the full dialogue plus sound cues, so it still covers the language.
const (
	VariantFull   = ""
	VariantForced = "forced"
	VariantSDH    = "sdh"
)

var (
	byToken = map[string]int{} // every accepted spelling → index into table
	byCode  = map[string]int{} // canonical code → index into table
)

func init() {
	for i, l := range table {
		byCode[l.Code] = i
		add := func(tok string) {
			if _, taken := byToken[tok]; !taken {
				byToken[tok] = i
			}
		}
		add(l.Code)
		if !l.Regional {
			// A regional row shares its base's 639-2 codes; those mean the base.
			add(l.ISO2B)
			add(l.ISO2T)
		}
		add(strings.ToLower(l.Name))
		for _, a := range l.aliases {
			add(a)
		}
	}
}

// All returns the whole table, base languages first in code order, then the regional
// variants. The slice is a copy.
func All() []Language {
	return append([]Language(nil), table...)
}

// norm lower-cases and trims a tag and spells region separators with a hyphen
// ("pt_BR" → "pt-br").
func norm(tok string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tok)), "_", "-")
}

// Canonical returns the canonical code for any spelling: "fre", "fra" and "French" → "fr";
// "pt-BR", "pt_br", "pob" and "Brazilian" → "pt-br". A region the table doesn't list folds
// to its base language ("en-US" → "en"). An empty or "und" tag returns "". A tag the table
// doesn't know comes back normalised but otherwise unchanged, so two equal unknown tags
// still compare equal — use Lookup to tell known from unknown.
func Canonical(tok string) string {
	n := norm(tok)
	if n == "" || n == "und" {
		return ""
	}
	if i, ok := byToken[n]; ok {
		return table[i].Code
	}
	if base, region, ok := strings.Cut(n, "-"); ok && isSubtags(region) {
		if i, ok := byToken[base]; ok && !table[i].Regional && len(base) <= 3 {
			return table[i].Code
		}
	}
	return n
}

// isSubtags reports whether s looks like the region/script part of a language tag:
// two-letter regions, three-digit areas ("419") and the common scripts, hyphenated
// ("us", "hant-tw"). It keeps most hyphenated words ("it-girl") from folding to a
// language; a two-letter one ("co-op") still looks like a region and does.
func isSubtags(s string) bool {
	for _, part := range strings.Split(s, "-") {
		switch {
		case len(part) == 2 && isAll(part, 'a', 'z'):
		case len(part) == 3 && isAll(part, '0', '9'):
		case scripts[part]:
		default:
			return false
		}
	}
	return true
}

var scripts = map[string]bool{"hans": true, "hant": true, "latn": true, "cyrl": true, "arab": true}

func isAll(s string, lo, hi rune) bool {
	for _, r := range s {
		if r < lo || r > hi {
			return false
		}
	}
	return true
}

// Lookup finds a language by any spelling Canonical accepts.
func Lookup(tok string) (Language, bool) {
	if i, ok := byCode[Canonical(tok)]; ok {
		return table[i], true
	}
	return Language{}, false
}

// Base returns the base language of a tag ("pt-br" → "pt", "fre" → "fr"). An unknown tag
// comes back as Canonical gives it.
func Base(tok string) string {
	if l, ok := Lookup(tok); ok {
		return l.ISO1
	}
	return Canonical(tok)
}

// Match reports whether a track (or file) tagged `track` is in the wanted language `want`.
// A base want matches every region of it ("pt" matches a "pt-br" track); a regional want
// matches only that region, never the plain base — callers that would accept the base as
// a fallback ask for it explicitly with Match(track, Base(want)). An untagged or "und"
// track never matches a specific language.
func Match(track, want string) bool {
	t, w := Canonical(track), Canonical(want)
	if t == "" || w == "" {
		return false
	}
	if t == w {
		return true
	}
	wl, ok := Lookup(w)
	return ok && !wl.Regional && Base(t) == w
}

// IsToken reports whether a name fragment is a language tag. Loose mode accepts every
// spelling the table knows (2- and 3-letter codes, names, regional tags). Strict mode is
// for sniffing free-form file names, where the 185 two-letter codes would match ordinary
// words ("it", "is", "to", "no"): it accepts only 3-letter codes and full names, and not
// the handful of those that are everyday English words ("cat", "hat", "sun").
func IsToken(tok string, strict bool) bool {
	n := norm(tok)
	if n == "" {
		return false
	}
	if _, ok := byToken[n]; !ok {
		if strict {
			return false
		}
		_, ok := Lookup(n) // "en-us": a region the table doesn't list, of a known base
		return ok
	}
	if !strict {
		return true
	}
	return len(n) >= 3 && !strings.Contains(n, "-") && !strictStop[n]
}

// strictStop are 3-letter codes and names that are also common English words, so a free
// file name containing them ("The.Cat.In.The.Hat") isn't read as Catalan or Haitian.
var strictStop = map[string]bool{
	"arg": true, "arm": true, "ave": true, "bel": true, "ben": true, "bis": true, "bos": true,
	"bur": true, "cat": true, "cha": true, "che": true, "cor": true, "cos": true, "dan": true,
	"div": true, "est": true, "ewe": true, "fil": true, "fin": true, "fry": true, "ful": true,
	"hat": true, "her": true, "hun": true, "ice": true, "ido": true, "ina": true, "ind": true,
	"kan": true, "kin": true, "lao": true, "lat": true, "lim": true, "lit": true, "lug": true,
	"mac": true, "mal": true, "mar": true, "may": true, "mon": true, "nor": true, "pan": true,
	"per": true, "pus": true, "rum": true, "run": true, "sag": true, "san": true, "sin": true,
	"slo": true, "sot": true, "spa": true, "sun": true, "tam": true, "tat": true, "tel": true,
	"ton": true, "twi": true, "ven": true, "wel": true,
}

// Name is the English name for a tag ("pt-br" → "Portuguese (Brazil)"). An unknown tag
// comes back trimmed, as given; an empty one as "".
func Name(tok string) string {
	if l, ok := Lookup(tok); ok {
		return l.Name
	}
	return strings.TrimSpace(tok)
}

// ISO1 is the two-letter base code whisper's -l wants ("fre" → "fr", "pt-br" → "pt"), or
// "" when the tag isn't a known language.
func ISO1(tok string) string {
	if l, ok := Lookup(tok); ok {
		return l.ISO1
	}
	return ""
}

// OpenSubtitles is the language code to search OpenSubtitles.com with. Mostly the
// canonical code; plain Portuguese and Chinese mean pt-pt and zh-cn there, and Latin
// American Spanish is "ea". An unknown tag is sent as given (normalised).
func OpenSubtitles(tok string) string {
	if l, ok := Lookup(tok); ok {
		if l.openSubs != "" {
			return l.openSubs
		}
		return l.Code
	}
	return norm(tok)
}

// SidecarTag is the language segment to write in a sidecar name: "<base>.<tag>.srt". It is
// the canonical code, so pt-br is written ".pt-br.srt" and read back as pt-br.
func SidecarTag(tok string) string {
	return Canonical(tok)
}

// SidecarName is what a subtitle file name says after the video's base name.
type SidecarName struct {
	Lang    string // canonical language; "" when there's no recognised language segment
	Variant string // VariantFull | VariantForced | VariantSDH
	// Token is the language segment as written (lower case), whether or not it is a
	// language: "eng" for ".eng.srt", "proper" for ".proper.srt", "" when the name has no
	// segment left once the qualifiers are peeled off (".srt", ".forced.srt"). Lang == ""
	// with Token != "" is a tag that is not a language — never credit it to one.
	Token string
}

// sidecarQualifiers are the trailing segments that describe a track rather than name its
// language, mapped to the variant they mean ("default" says nothing about it).
var sidecarQualifiers = map[string]string{
	"forced": VariantForced, "sdh": VariantSDH, "hi": VariantSDH, "cc": VariantSDH, "default": VariantFull,
}

// ParseSidecarTag reads the language and variant from the dotted segments after a
// sidecar's base name, the way Plex and Bazarr name them: ["en"] → en, ["en","forced"] →
// en forced, ["en","hi"] → en SDH (Bazarr's hearing-impaired), ["hi"] → Hindi,
// ["pt-br"] → pt-br. Trailing qualifiers are peeled off first; "hi" only counts as one
// when a language sits before it, since on its own it is Hindi. Forced wins over SDH: a
// forced track is never the full subtitle, whatever else it is. The segment left at the
// end is the language when the table knows it.
func ParseSidecarTag(segs []string) SidecarName {
	var out SidecarName
	i := len(segs) - 1
	for ; i >= 0; i-- {
		seg := norm(segs[i])
		v, ok := sidecarQualifiers[seg]
		if !ok {
			break
		}
		if seg == "hi" && (i == 0 || !IsToken(segs[i-1], false)) {
			break // a bare "hi" is the language, Hindi
		}
		switch {
		case v == VariantForced:
			out.Variant = VariantForced
		case v == VariantSDH && out.Variant == VariantFull:
			out.Variant = VariantSDH
		}
	}
	if i >= 0 {
		out.Token = norm(segs[i])
		if IsToken(out.Token, false) {
			out.Lang = Canonical(out.Token)
		}
	}
	return out
}
