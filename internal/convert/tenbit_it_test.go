//go:build linux

package convert

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTenBitOutputIT encodes a synthetic 8-bit H.264 clip with the real encoders and probes
// the result: "match the source" writes 8-bit, "always" writes 10-bit. Synthetic media only
// (lavfi); it needs the app image's ffmpeg, so it runs only with ARRMADA_FFMPEG_IT set.
func TestTenBitOutputIT(t *testing.T) {
	if os.Getenv("ARRMADA_FFMPEG_IT") == "" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 inside the app image to run real encodes")
	}
	ctx := context.Background()
	dir := t.TempDir()
	ff := func(args ...string) {
		t.Helper()
		out, err := exec.Command("ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	src := filepath.Join(dir, "src.mkv")
	ff("-f", "lavfi", "-i", "testsrc2=s=640x360:r=24:d=3", "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", src)
	mi, err := probe(ctx, "ffprobe", src)
	if err != nil {
		t.Fatal(err)
	}
	if mi.TenBit {
		t.Fatal("the synthetic source should be 8-bit")
	}
	for _, c := range []struct {
		codec    string
		match    bool
		want10   bool
		describe string
	}{
		{"hevc", true, false, "HEVC, match source"},
		{"hevc", false, true, "HEVC, always"},
		{"av1", true, false, "AV1, match source"},
	} {
		out := filepath.Join(dir, fmt.Sprintf("out-%s-%v.mkv", c.codec, c.match))
		args := []string{"-i", src}
		args = append(args, compileOutputArgs(cpuEncoder(c.codec), mi, Plan{VideoCodec: c.codec, Quality: 30, MatchSourceDepth: c.match}, false, 2, true)...)
		ff(append(args, out)...)
		got, err := probe(ctx, "ffprobe", out)
		if err != nil {
			t.Fatal(err)
		}
		if got.TenBit != c.want10 {
			t.Errorf("%s: output ten_bit = %v, want %v", c.describe, got.TenBit, c.want10)
		}
	}
}
