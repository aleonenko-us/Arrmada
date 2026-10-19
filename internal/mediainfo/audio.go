package mediainfo

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// AudioInfo is what ffprobe says about an audio file: its first audio stream, the
// container's length and tags, and any chapter marks. Music reads the codec, bit depth,
// sample rate and bitrate to tell what tier a file really is; the audiobook server reads
// the length, tags and chapters.
type AudioInfo struct {
	Codec string `json:"codec"` // ffprobe's codec_name: "flac", "mp3", "aac", "pcm_s16le"…
	// BitrateKbps is the audio stream's bitrate, or the container's when the stream
	// doesn't say (FLAC usually doesn't). 0 when neither does.
	BitrateKbps int `json:"bitrate_kbps"`
	// FormatBitrate is the container's bitrate in bits per second, as ffprobe gives it.
	FormatBitrate int `json:"format_bitrate"`
	// BitsPerSample is the stream's real sample depth (16, 24), 0 when unknown or when the
	// codec has none (lossy codecs).
	BitsPerSample int     `json:"bits_per_sample"`
	SampleRateHz  int     `json:"sample_rate_hz"`
	Channels      int     `json:"channels"`
	DurationSec   float64 `json:"duration_sec"`
	// Tags are the container's tags, keys as ffprobe prints them.
	Tags     map[string]string `json:"tags,omitempty"`
	Chapters []AudioChapter    `json:"chapters,omitempty"`
}

// AudioChapter is one chapter mark, in seconds.
type AudioChapter struct {
	Start, End float64
	Tags       map[string]string
}

// audioProbeArgs is the one ffprobe command line for audio files: the container, the first
// audio stream and the chapters, as JSON.
func audioProbeArgs(path string) []string {
	return []string{"-v", "error", "-print_format", "json",
		"-show_format", "-show_chapters", "-show_streams", "-select_streams", "a:0", path}
}

// ErrNoAudio is returned for a file ffprobe reads but finds no audio stream in.
var ErrNoAudio = errors.New("no audio stream")

// audioProbeTimeout bounds one probe; a single audio file answers in well under a second.
const audioProbeTimeout = 90 * time.Second

// ProbeAudio runs ffprobe on an audio file (music's tier check). It only reads the file.
func ProbeAudio(ctx context.Context, path string) (AudioInfo, error) {
	info, err := ProbeAudioWith(ctx, "ffprobe", path)
	if err == nil && info.Codec == "" {
		return info, ErrNoAudio
	}
	return info, err
}

// ProbeAudioWith is ProbeAudio with a given ffprobe binary (the audiobook server's is
// configurable). It runs through the tool runner at normal priority, since someone is
// usually waiting on the answer, and a file with no audio stream is not an error here:
// it comes back with an empty Codec.
func ProbeAudioWith(ctx context.Context, ffprobe, path string) (AudioInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, audioProbeTimeout)
	defer cancel()
	out, err := toolrun.Command(ctx, toolrun.Normal, ffprobe, audioProbeArgs(path)...).Output()
	if err != nil {
		return AudioInfo{}, err
	}
	return ParseAudio(out)
}

// ParseAudio reads ffprobe's JSON (AudioProbeArgs) into an AudioInfo. A file with no audio
// stream parses without error and an empty Codec.
func ParseAudio(out []byte) (AudioInfo, error) {
	var r struct {
		Format struct {
			Duration string            `json:"duration"`
			BitRate  string            `json:"bit_rate"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			CodecType        string `json:"codec_type"`
			CodecName        string `json:"codec_name"`
			BitRate          string `json:"bit_rate"`
			SampleRate       string `json:"sample_rate"`
			SampleFmt        string `json:"sample_fmt"`
			Channels         int    `json:"channels"`
			BitsPerSample    int    `json:"bits_per_sample"`
			BitsPerRawSample string `json:"bits_per_raw_sample"`
		} `json:"streams"`
		Chapters []struct {
			StartTime string            `json:"start_time"`
			EndTime   string            `json:"end_time"`
			Tags      map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return AudioInfo{}, err
	}
	var info AudioInfo
	info.DurationSec, _ = strconv.ParseFloat(r.Format.Duration, 64)
	info.FormatBitrate, _ = strconv.Atoi(r.Format.BitRate)
	info.Tags = r.Format.Tags
	for _, s := range r.Streams {
		if s.CodecType != "" && s.CodecType != "audio" {
			continue
		}
		info.Codec = strings.ToLower(s.CodecName)
		info.Channels = s.Channels
		info.SampleRateHz, _ = strconv.Atoi(s.SampleRate)
		if br, _ := strconv.Atoi(s.BitRate); br > 0 {
			info.BitrateKbps = kbps(br)
		}
		info.BitsPerSample = sampleDepth(s.BitsPerRawSample, s.BitsPerSample, s.SampleFmt)
		break
	}
	if info.BitrateKbps == 0 && info.FormatBitrate > 0 {
		info.BitrateKbps = kbps(info.FormatBitrate)
	}
	for _, c := range r.Chapters {
		start, _ := strconv.ParseFloat(c.StartTime, 64)
		end, _ := strconv.ParseFloat(c.EndTime, 64)
		info.Chapters = append(info.Chapters, AudioChapter{Start: start, End: end, Tags: c.Tags})
	}
	return info, nil
}

// kbps rounds bits per second to the nearest kbps.
func kbps(bps int) int { return (bps + 500) / 1000 }

// sampleDepth is a stream's real bit depth. bits_per_raw_sample is the honest figure (a
// 24-bit FLAC decodes into 32-bit samples, so sample_fmt says s32); the others are
// fallbacks. Floating-point sample formats are what lossy decoders produce and say nothing
// about the source, so they read as 0.
func sampleDepth(raw string, perSample int, sampleFmt string) int {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	if perSample > 0 {
		return perSample
	}
	switch strings.TrimSuffix(sampleFmt, "p") {
	case "u8":
		return 8
	case "s16":
		return 16
	case "s32":
		return 24 // 24-bit audio is carried in 32-bit samples; true 32-bit is rarer still
	}
	return 0
}
