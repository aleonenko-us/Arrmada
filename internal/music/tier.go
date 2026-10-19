package music

import (
	"strings"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
)

// TierFromProbe is the tier a file really is, from what ffprobe measured in it, rather
// than what the release name claimed. A release labelled "FLAC" that holds MP3s reads as
// the MP3 tier it is.
//
//   - Lossless codecs by name: FLAC (FLAC-24 when the source is 24-bit or sampled above
//     48 kHz), ALAC, WAV for any PCM (AIFF included), and the other lossless codecs at the
//     FLAC tier.
//   - MP3 (and WMA) by bitrate, see lossyTier.
//   - AAC at 230 kbps or more is AAC-256; below that it takes the MP3 rung of the same
//     bitrate, because the ladder has no lower AAC rung.
//   - Opus and Vorbis are their own rungs.
//
// Unknown when the codec isn't an audio codec this ladder covers, or a lossy file states no
// bitrate.
func TierFromProbe(a mediainfo.AudioInfo) Quality {
	codec := strings.ToLower(a.Codec)
	hiRes := a.BitsPerSample >= 24 || a.SampleRateHz > 48000
	switch {
	case codec == "flac", codec == "ape", codec == "wavpack", codec == "tta", codec == "tak",
		codec == "wmalossless", codec == "mlp", codec == "truehd":
		if hiRes {
			return QualityFLAC24
		}
		return QualityFLAC
	case codec == "alac":
		return QualityALAC
	case strings.HasPrefix(codec, "pcm_"):
		return QualityWAV
	case codec == "opus":
		return QualityOpus
	case codec == "vorbis":
		return QualityOGG
	case codec == "aac" || codec == "aac_latm":
		return lossyTier(a.BitrateKbps, true)
	case codec == "mp3" || codec == "mp3float" || codec == "mp2" || codec == "wmav1" || codec == "wmav2":
		return lossyTier(a.BitrateKbps, false)
	}
	return QualityUnknown
}
