package music

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
)

// MUS-11: the tier a file really is, from what ffprobe measured.
func TestTierFromProbe(t *testing.T) {
	cases := []struct {
		name string
		in   mediainfo.AudioInfo
		want Quality
	}{
		{"FLAC 16/44.1", mediainfo.AudioInfo{Codec: "flac", BitsPerSample: 16, SampleRateHz: 44100, BitrateKbps: 900}, QualityFLAC},
		{"FLAC 24/96", mediainfo.AudioInfo{Codec: "flac", BitsPerSample: 24, SampleRateHz: 96000}, QualityFLAC24},
		{"FLAC 16/96", mediainfo.AudioInfo{Codec: "flac", BitsPerSample: 16, SampleRateHz: 96000}, QualityFLAC24},
		{"ALAC", mediainfo.AudioInfo{Codec: "alac", BitsPerSample: 16, SampleRateHz: 44100}, QualityALAC},
		{"WAV", mediainfo.AudioInfo{Codec: "pcm_s16le", BitsPerSample: 16, SampleRateHz: 44100}, QualityWAV},
		{"AIFF", mediainfo.AudioInfo{Codec: "pcm_s24be", BitsPerSample: 24, SampleRateHz: 48000}, QualityWAV},
		{"WavPack", mediainfo.AudioInfo{Codec: "wavpack", BitsPerSample: 16, SampleRateHz: 44100}, QualityFLAC},
		{"MP3 CBR 320", mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 320}, QualityMP3320},
		{"MP3 V0", mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 245}, QualityMP3V0},
		{"MP3 CBR 256", mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 256}, QualityMP3256},
		{"MP3 V2", mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 190}, QualityMP3V2},
		{"MP3 CBR 192", mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 192}, QualityMP3192},
		{"MP3 CBR 128", mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 128}, QualityMP3128},
		{"MP3 no bitrate", mediainfo.AudioInfo{Codec: "mp3"}, QualityUnknown},
		{"AAC 256", mediainfo.AudioInfo{Codec: "aac", BitrateKbps: 256}, QualityAAC256},
		{"AAC 128", mediainfo.AudioInfo{Codec: "aac", BitrateKbps: 128}, QualityMP3128},
		{"Opus", mediainfo.AudioInfo{Codec: "opus", BitrateKbps: 160}, QualityOpus},
		{"Vorbis", mediainfo.AudioInfo{Codec: "vorbis", BitrateKbps: 192}, QualityOGG},
		{"not audio", mediainfo.AudioInfo{Codec: "mjpeg"}, QualityUnknown},
	}
	for _, c := range cases {
		if got := TierFromProbe(c.in); got != c.want {
			t.Errorf("%s: TierFromProbe = %q, want %q", c.name, got, c.want)
		}
	}
}
