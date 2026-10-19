package music

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
)

// MUS-11 against real files: synthetic tones made by ffmpeg in a temp dir, probed with
// ffprobe, read as tiers. Skipped where ffmpeg isn't installed, unless ARRMADA_FFMPEG_IT=1
// says it must run (the Docker test image has ffmpeg).
func TestTierFromProbeSyntheticFiles(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil || !mediainfo.Available() {
		if os.Getenv("ARRMADA_FFMPEG_IT") == "1" {
			t.Fatal("ARRMADA_FFMPEG_IT=1 but ffmpeg/ffprobe aren't on PATH")
		}
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	cases := []struct {
		file string
		args []string
		want Quality
	}{
		{"cd.flac", []string{"-ar", "44100", "-sample_fmt", "s16", "-c:a", "flac"}, QualityFLAC},
		{"hires.flac", []string{"-ar", "96000", "-sample_fmt", "s32", "-bits_per_raw_sample", "24", "-c:a", "flac"}, QualityFLAC24},
		{"cbr320.mp3", []string{"-ar", "44100", "-c:a", "libmp3lame", "-b:a", "320k"}, QualityMP3320},
		{"cbr128.mp3", []string{"-ar", "44100", "-c:a", "libmp3lame", "-b:a", "128k"}, QualityMP3128},
		{"pcm.wav", []string{"-ar", "44100", "-c:a", "pcm_s16le"}, QualityWAV},
		{"tone.opus", []string{"-ar", "48000", "-c:a", "libopus", "-b:a", "128k"}, QualityOpus},
	}
	for _, c := range cases {
		out := filepath.Join(dir, c.file)
		args := append([]string{"-hide_banner", "-loglevel", "error", "-f", "lavfi",
			"-i", "sine=frequency=440:duration=3", "-ac", "2"}, c.args...)
		if b, err := exec.Command(ffmpeg, append(args, out)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: ffmpeg: %v\n%s", c.file, err, b)
		}
		info, err := mediainfo.ProbeAudio(context.Background(), out)
		if err != nil {
			t.Fatalf("%s: probe: %v", c.file, err)
		}
		if got := TierFromProbe(info); got != c.want {
			t.Errorf("%s: tier %q, want %q (probed %+v)", c.file, got, c.want, info)
		}
	}
}
