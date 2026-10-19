package music

import (
	"path/filepath"
	"strings"
)

// Audio formats, uppercase. Used to classify releases and files on disk, and — via a
// profile's format_scores — to decide which releases are wanted.
//
// Lossless and lossy are tracked separately because the distinction is what a music quality
// profile is actually about: a FLAC is not "a bigger MP3", it's a different tier, and an
// upgrade from lossy to lossless is always worth taking regardless of bitrate.
var losslessFormats = map[string]bool{
	"FLAC": true, "ALAC": true, "WAV": true, "AIFF": true, "APE": true, "WV": true,
}

var lossyFormats = map[string]bool{
	"MP3": true, "AAC": true, "M4A": true, "OGG": true, "OPUS": true, "WMA": true,
}

// audioExts maps a file extension to its format tag.
var audioExts = map[string]string{
	".flac": "FLAC", ".alac": "ALAC", ".wav": "WAV", ".aiff": "AIFF", ".aif": "AIFF",
	".ape": "APE", ".wv": "WV",
	".mp3": "MP3", ".aac": "AAC", ".m4a": "M4A", ".ogg": "OGG", ".opus": "OPUS", ".wma": "WMA",
}

// IsLossless reports whether a format tag is a lossless codec.
func IsLossless(f string) bool { return losslessFormats[strings.ToUpper(f)] }

// IsLossy reports whether a format tag is a lossy codec.
func IsLossy(f string) bool { return lossyFormats[strings.ToUpper(f)] }

// IsAudioFormat reports whether a tag names an audio format we understand.
func IsAudioFormat(f string) bool {
	u := strings.ToUpper(f)
	return losslessFormats[u] || lossyFormats[u]
}

// IsAudioFile reports whether a path looks like an audio file by extension.
func IsAudioFile(path string) bool {
	_, ok := audioExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// FormatOf returns the uppercase format tag for a file path ("" when not audio).
func FormatOf(path string) string {
	return audioExts[strings.ToLower(filepath.Ext(path))]
}

// HeldTier reads the tier of a track already on disk from what the library stored for it.
// The format column holds two kinds of value: a tier name ("MP3-320", "FLAC-24"), written by
// imports and by probing, and an extension tag ("MP3", "FLAC"), written by older library
// scans. An extension tag is turned into the tier it must be: the lossless ones directly,
// a lossy one through its bitrate when one is known. A lossy file with no bitrate on record
// is unknown, not guessed: guessing low would let a worse copy replace it, guessing high
// would keep a bad one forever.
func HeldTier(format string, bitrateKbps int) Quality {
	f := strings.ToUpper(strings.TrimSpace(format))
	if q := Quality(f); q != QualityUnknown && qualityRank[q] > 0 {
		return q
	}
	switch f {
	case "AIFF":
		return QualityWAV // uncompressed, like WAV, and as poorly tagged
	case "APE", "WV":
		return QualityFLAC // the other lossless codecs sit at the lossless tier
	case "OPUS":
		return QualityOpus
	case "OGG":
		return QualityOGG
	case "MP3", "WMA":
		return lossyTier(bitrateKbps, false)
	case "AAC":
		return lossyTier(bitrateKbps, true)
	}
	// "M4A" could be AAC or ALAC; without a probe there's no telling which.
	return QualityUnknown
}

// lossyTier places a lossy file on the ladder by its bitrate in kbps. The bands are wide on
// purpose: VBR files only have an average, and a tier that wobbled across a band edge
// would make the upgrade path churn on borderline files.
//
// A constant-bitrate MP3 measures exactly a standard rate, so an exact 256 is CBR 256 and
// an exact 192 or 224 is CBR 192-class; anything else between the standard rates is VBR:
// 210 kbps and up averages like LAME V0, 150 and up like V2. AAC at 230 kbps or more is
// AAC-256; below that it takes the MP3 rung of the same bitrate, because the ladder has no
// lower AAC rung.
func lossyTier(kbps int, aac bool) Quality {
	if kbps <= 0 {
		return QualityUnknown
	}
	if aac && kbps >= 230 {
		return QualityAAC256
	}
	switch {
	case kbps >= 315:
		return QualityMP3320
	case kbps == 256 && !aac:
		return QualityMP3256
	case kbps == 224 || kbps == 192:
		return QualityMP3192
	case kbps == 160 || kbps == 128 || kbps < 150:
		return QualityMP3128
	case kbps >= 210 && !aac:
		return QualityMP3V0
	case kbps >= 170 && aac:
		return QualityMP3192
	case !aac:
		return QualityMP3V2
	}
	return QualityMP3128
}

// BetterTier reports whether a candidate file is worth putting in place of the one held:
// strictly higher on the ladder. Lossless never replaces lossless (FLAC over ALAC, or over
// another FLAC, is churn with nothing gained), an unknown candidate never replaces
// anything, and anything known replaces a held file whose tier is unknown.
func BetterTier(cand, held Quality) bool {
	switch {
	case cand == QualityUnknown:
		return false
	case held == QualityUnknown:
		return true
	case IsLosslessQuality(cand) && IsLosslessQuality(held):
		return false
	}
	return QualityRank(cand) > QualityRank(held)
}
