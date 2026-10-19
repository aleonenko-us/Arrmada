package music

import "testing"

func TestFormatClassification(t *testing.T) {
	for _, f := range []string{"FLAC", "flac", "ALAC", "WAV"} {
		if !IsLossless(f) || IsLossy(f) {
			t.Errorf("%q should be lossless", f)
		}
	}
	for _, f := range []string{"MP3", "opus", "AAC", "OGG"} {
		if !IsLossy(f) || IsLossless(f) {
			t.Errorf("%q should be lossy", f)
		}
	}
	if IsAudioFormat("MKV") {
		t.Error("MKV is not an audio format")
	}
	if got := FormatOf("/music/a/b/01 - Airbag.FLAC"); got != "FLAC" {
		t.Errorf("FormatOf = %q, want FLAC (extension case shouldn't matter)", got)
	}
	if FormatOf("/music/cover.jpg") != "" || IsAudioFile("/music/cover.jpg") {
		t.Error("a jpg is not an audio file")
	}
}

// The held tier reads both kinds of stored value: tier names written by imports and probes,
// and the extension tags older scans wrote, which need a bitrate to mean anything for lossy.
func TestHeldTier(t *testing.T) {
	cases := []struct {
		format  string
		bitrate int
		want    Quality
	}{
		{"MP3-320", 0, QualityMP3320},
		{"FLAC-24", 0, QualityFLAC24},
		{"flac", 0, QualityFLAC},
		{"ALAC", 0, QualityALAC},
		{"WAV", 0, QualityWAV},
		{"AIFF", 0, QualityWAV},
		{"APE", 0, QualityFLAC},
		{"MP3", 320, QualityMP3320},
		{"MP3", 245, QualityMP3V0},
		{"MP3", 256, QualityMP3256},
		{"MP3", 128, QualityMP3128},
		{"MP3", 0, QualityUnknown}, // a lossy file with no bitrate on record isn't guessed at
		{"AAC", 256, QualityAAC256},
		{"OPUS", 0, QualityOpus},
		{"M4A", 256, QualityUnknown}, // AAC or ALAC — can't tell without a probe
		{"", 0, QualityUnknown},
	}
	for _, c := range cases {
		if got := HeldTier(c.format, c.bitrate); got != c.want {
			t.Errorf("HeldTier(%q, %d) = %q, want %q", c.format, c.bitrate, got, c.want)
		}
	}
}

// The point of the tier: a lossless file is not "a bigger MP3". Lossless always beats
// lossy, lossless never churns against lossless, a worse copy never replaces a better one,
// and a known tier beats an unknown one.
func TestBetterTier(t *testing.T) {
	cases := []struct {
		cand, held Quality
		want       bool
	}{
		{QualityFLAC, QualityMP3320, true},
		{QualityMP3320, QualityFLAC, false},
		{QualityFLAC, QualityALAC, false}, // lossless vs lossless: churn, nothing gained
		{QualityFLAC24, QualityFLAC, false},
		{QualityMP3320, QualityMP3128, true},
		{QualityMP3256, QualityMP3V0, false},
		{QualityMP3320, QualityMP3320, false},
		{QualityMP3128, QualityUnknown, true}, // anything known beats a held unknown
		{QualityUnknown, QualityMP3128, false},
		{QualityUnknown, QualityUnknown, false},
	}
	for _, c := range cases {
		if got := BetterTier(c.cand, c.held); got != c.want {
			t.Errorf("BetterTier(%q, %q) = %v, want %v", c.cand, c.held, got, c.want)
		}
	}
	if !BetterTier(HeldTier("MP3", 320), HeldTier("MP3", 128)) || BetterTier(HeldTier("FLAC", 0), HeldTier("ALAC", 0)) {
		t.Error("held tags must compare through HeldTier")
	}
}
