package filecheck

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// The real ffprobe and ffmpeg on generated files: sound, silence and no audio at all.
// Skipped where they aren't installed.
func TestFFmpegProbeOnRealFiles(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	if testing.Short() {
		t.Skip("encodes test files")
	}
	dir := t.TempDir()
	gen := func(name string, audio []string, lang string) string {
		out := filepath.Join(dir, name)
		args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x36:d=90:r=5"}
		args = append(args, audio...)
		args = append(args, "-t", "90", "-c:v", "libx264", "-preset", "ultrafast")
		if len(audio) > 0 {
			args = append(args, "-c:a", "aac", "-metadata:s:a:0", "language="+lang)
		}
		args = append(args, "-y", out)
		if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("make %s: %v\n%s", name, err, b)
		}
		return out
	}
	tone := gen("tone.mkv", []string{"-f", "lavfi", "-i", "sine=frequency=440:duration=90"}, "eng")
	quiet := gen("silent.mkv", []string{"-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo"}, "rus")
	mute := gen("mute.mkv", nil, "")

	ctx := context.Background()
	in := Input{Kind: KindMovie, RuntimeMin: 0, OrigLanguage: "en"}
	if got := Classify(in, FFmpeg{}.Probe(ctx, tone)); len(got) != 0 {
		t.Errorf("tone: %+v", got)
	}
	p := FFmpeg{}.Probe(ctx, quiet)
	if got := codes(Classify(in, p)); got != CodeSilentAudio {
		t.Errorf("silence: %q (probe %+v)", got, p)
	}
	if got := codes(Classify(in, FFmpeg{}.Probe(ctx, mute))); got != CodeNoAudio {
		t.Errorf("no audio: %q", got)
	}
	// A 90-second file for a 100-minute film is cut short.
	if got := codes(Classify(Input{Kind: KindMovie, RuntimeMin: 100}, FFmpeg{}.Probe(ctx, tone))); got != CodeUnreadable {
		t.Errorf("short: %q", got)
	}
	if got := codes(Classify(in, FFmpeg{}.Probe(ctx, filepath.Join(dir, "missing.mkv")))); got != CodeUnreadable {
		t.Errorf("unreadable: %q", got)
	}
}
