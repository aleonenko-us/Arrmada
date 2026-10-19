package music

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Quality is an audio quality TIER — codec and bitrate together.
//
// The tier, not the codec, is what a music quality profile is about: "MP3" on its own says
// nothing, because MP3-128 and MP3-320 are two and a half times apart. Unlike books — where
// EPUB vs MOBI is a preference with no better/worse — these are genuinely ordered, so they
// form a ladder the way video resolutions do.
//
// The names are the ones trackers actually print, so a profile reads like the releases do.
type Quality string

const (
	QualityUnknown Quality = ""
	QualityMP3128  Quality = "MP3-128"
	QualityMP3192  Quality = "MP3-192"
	QualityOGG     Quality = "OGG"
	QualityOpus    Quality = "OPUS"
	QualityMP3V2   Quality = "MP3-V2"
	QualityMP3256  Quality = "MP3-256"
	QualityAAC256  Quality = "AAC-256"
	QualityMP3V0   Quality = "MP3-V0"
	QualityMP3320  Quality = "MP3-320"
	QualityWAV     Quality = "WAV"
	QualityALAC    Quality = "ALAC"
	QualityFLAC    Quality = "FLAC"
	QualityFLAC24  Quality = "FLAC-24"
)

// qualityRank orders the tiers. Lossless outranks every lossy tier, and hi-res lossless
// tops it. WAV sits below FLAC/ALAC despite being uncompressed: it's the same audio in a
// container that carries no tags, which makes it strictly worse to keep in a library.
var qualityRank = map[Quality]int{
	QualityUnknown: 0,
	QualityMP3128:  1,
	QualityMP3192:  2,
	QualityOGG:     3,
	QualityOpus:    4,
	QualityMP3V2:   5,
	QualityMP3256:  6,
	QualityAAC256:  7,
	QualityMP3V0:   8,
	QualityMP3320:  9,
	QualityWAV:     10,
	QualityALAC:    11,
	QualityFLAC:    12,
	QualityFLAC24:  13,
}

// AllQualities lists every tier best-first — the score-able set a profile is built from.
var AllQualities = []Quality{
	QualityFLAC24, QualityFLAC, QualityALAC, QualityWAV,
	QualityMP3320, QualityMP3V0, QualityAAC256, QualityMP3256,
	QualityMP3V2, QualityOpus, QualityOGG, QualityMP3192, QualityMP3128,
}

// QualityRank returns a tier's position in the ladder (0 = unknown).
func QualityRank(q Quality) int { return qualityRank[q] }

// IsLosslessQuality reports whether a tier is lossless.
func IsLosslessQuality(q Quality) bool {
	switch q {
	case QualityFLAC24, QualityFLAC, QualityALAC, QualityWAV:
		return true
	}
	return false
}

var (
	// Hi-res markers. 24-bit is the real signal; the sample rates are corroboration.
	reHiRes = regexp.MustCompile(`(?i)\b(24[\s-]?bits?|24b|hi[\s-]?res|96\s?khz|88\.2\s?khz|176\s?khz|192\s?khz)\b`)
	// LAME VBR presets. V0 ≈ 245 kbps and V2 ≈ 190, which is why they sit either side of 256.
	reVBRPreset = regexp.MustCompile(`(?i)\b(?:lame\s*)?-?V([0-9])\b`)
	// A bitrate carrying a unit or a mode word: "320kbps", "CBR 320".
	reBitrate = regexp.MustCompile(`(?i)\b(\d{2,4})\s?k(?:bps)?\b|\b(?:cbr|abr|vbr)\s?(\d{2,4})\b`)
	// A bare bitrate. Restricted to the standard MP3/AAC rates rather than any 3-digit run,
	// so a catalogue number or a name like "Blink-182" isn't read as a bitrate. "MP3-320" and
	// "[MP3 256]" carry no unit at all and are the common shapes, so this is what actually
	// catches most releases.
	reStdBitrate = regexp.MustCompile(`\b(96|112|128|160|192|224|256|320)\b`)
)

// reFlacN is a codec token with its bit depth run on: "FLAC24", "flac16".
var reFlacN = regexp.MustCompile(`^flac(\d+)$`)

// DetectQuality reads the audio tier from a release name.
//
// The name is read as whole tokens (split on anything that isn't a letter or a digit), never
// as substrings. Substring matching read "Escape" and "Roberta Flack" as FLAC, "Palace" as
// ALAC, "New Wave" as WAV, "Mixtape" as APE, "Isaac" as AAC and "Doggystyle" as OGG, so a
// "Lossless only" profile grabbed MP3s and they outranked real FLAC on seeders. A name that
// says only "Lossless" with no codec now reads as unknown, and is refused: the
// conservative answer. A title word that is exactly a codec ("Ape") still reads as one;
// that's rare, and the tier is shown wherever a release is.
//
// Lossless markers are checked first: a release tagged "FLAC" is lossless whatever else the
// name contains, and a name like "FLAC / MP3 320" is a dual-format upload whose better half
// is what we'd take.
func DetectQuality(name string) Quality {
	name = reCodecRunOn.ReplaceAllString(name, "$1 $2")
	tokens := map[string]bool{}
	flacDepth := 0
	for _, tok := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		tokens[tok] = true
		if m := reFlacN.FindStringSubmatch(tok); m != nil {
			tokens["flac"] = true
			if n, _ := strconv.Atoi(m[1]); n > flacDepth {
				flacDepth = n
			}
		}
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if tokens[w] {
				return true
			}
		}
		return false
	}
	switch {
	case has("flac"):
		if flacDepth >= 24 || reHiRes.MatchString(name) {
			return QualityFLAC24
		}
		return QualityFLAC
	case has("alac"):
		return QualityALAC
	case has("ape", "wv", "wavpack", "tak"):
		return QualityFLAC // other lossless codecs sit at the lossless tier
	case has("wav"):
		return QualityWAV
	}

	// Lossy: a VBR preset is more specific than a raw number, so check it first.
	if m := reVBRPreset.FindStringSubmatch(name); m != nil {
		switch m[1] {
		case "0":
			return QualityMP3V0
		case "1", "2":
			return QualityMP3V2
		}
	}
	aac := has("aac", "m4a")
	switch br := detectBitrate(name); {
	case br >= 320:
		return QualityMP3320
	case br >= 256:
		if aac {
			return QualityAAC256
		}
		return QualityMP3256
	case br >= 192:
		return QualityMP3192
	case br > 0:
		return QualityMP3128
	}
	// No bitrate stated — fall back to the codec's typical tier.
	switch {
	case has("opus"):
		return QualityOpus
	case has("ogg", "vorbis"):
		return QualityOGG
	case aac:
		return QualityAAC256
	case has("mp3"):
		return QualityMP3192 // an untagged MP3 is a coin toss; assume the middle, not the best
	}
	return QualityUnknown
}

// reCodecRunOn is a lossy codec with its rate or preset run on: "MP3320", "MP3V0",
// "AAC256". DetectQuality splits it into the codec and a separate rate or preset first.
var reCodecRunOn = regexp.MustCompile(`(?i)\b(mp3|aac)(v\d|\d{2,3})\b`)

// detectBitrate pulls a kbps figure out of a release name (0 when absent).
func detectBitrate(name string) int {
	if m := reBitrate.FindStringSubmatch(name); m != nil {
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			if n, err := strconv.Atoi(g); err == nil && n >= 32 && n <= 3000 {
				return n
			}
		}
	}
	if m := reStdBitrate.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// Release is what a music release name tells us.
type Release struct {
	Artist  string  `json:"artist,omitempty"`
	Album   string  `json:"album,omitempty"`
	Year    int     `json:"year,omitempty"`
	Quality Quality `json:"quality,omitempty"`
	// Discography marks a release covering an artist's whole catalogue rather than one
	// album — grabbing one against a single album would fill that album's folder with
	// everything the artist ever made.
	Discography bool `json:"discography,omitempty"`
}

var (
	reYearTag       = regexp.MustCompile(`[\(\[\{]?\b(19\d{2}|20\d{2})\b[\)\]\}]?`)
	reDiscographyTG = regexp.MustCompile(`(?i)\b(discography|complete\s+collection|all\s+albums|box\s?set|anthology)\b`)
	// Bracketed tag groups: "[FLAC]", "(2016)", "{WEB}" — noise once the year and quality
	// have been read off, and they wreck a title comparison if left in.
	reBracketed = regexp.MustCompile(`[\(\[\{][^\)\]\}]*[\)\]\}]`)
)

// ParseRelease reads artist, album, year and quality out of a music release name.
//
// Music releases are overwhelmingly "Artist - Album (Year) [Quality]", with the separator
// sometimes a dot. This handles that shape and degrades gracefully: an unparsed album is
// empty rather than wrong, so a caller can refuse to place it instead of guessing.
func ParseRelease(name string) Release {
	r := Release{Quality: DetectQuality(name), Discography: reDiscographyTG.MatchString(name)}
	if m := reYearTag.FindStringSubmatch(name); m != nil {
		r.Year, _ = strconv.Atoi(m[1])
	}

	clean := reBracketed.ReplaceAllString(name, " ")
	clean = strings.TrimSuffix(clean, ".mkv")
	// Dots as separators, but only when the name has no spaces at all — "Mr. Bungle" must
	// keep its dot, while "Artist.Album.2016.FLAC" needs them split.
	if !strings.Contains(clean, " ") {
		clean = strings.ReplaceAll(clean, ".", " ")
	}
	clean = reYearTag.ReplaceAllString(clean, " ")

	parts := strings.SplitN(clean, " - ", 2)
	if len(parts) == 2 {
		r.Artist = tidy(parts[0])
		r.Album = tidy(parts[1])
		return r
	}
	r.Artist = tidy(clean)
	return r
}

// tidy trims separator noise and collapses whitespace.
func tidy(s string) string {
	s = strings.Trim(s, " -_.")
	return strings.Join(strings.Fields(s), " ")
}

// NormKey folds a title for tolerant whole-string comparison (scan keys, track titles):
// foldMusic with the spaces taken out, so "Björk" and "Bjork" key the same, "&" and "and"
// do too, and a title in another script keys as itself rather than as nothing.
func NormKey(s string) string {
	return strings.ReplaceAll(foldMusic(s), " ", "")
}
