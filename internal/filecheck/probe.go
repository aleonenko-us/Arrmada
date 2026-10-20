package filecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// Prober reads a file (FFmpeg; tests fake it).
type Prober interface {
	Probe(ctx context.Context, path string) Probe
}

// How the silence check samples: windowSec seconds at each of windowAt (fractions of the
// length). Three short windows rather than the whole film: a few seconds of decoding at
// idle priority instead of two hours of it.
var windowAt = []float64{0.25, 0.5, 0.75}

// Where the picture check looks for video packets: a file can claim the film's full length
// while its video track stops partway (a broken upload), and players then freeze there.
// Reading packets is cheap — nothing is decoded.
var videoAt = []float64{0.25, 0.5, 0.75, 0.95}

const (
	windowSec     = 20
	probeTimeout  = 60 * time.Second
	windowTimeout = 2 * time.Minute
)

// FFmpeg probes with the system's ffprobe and ffmpeg, at idle CPU and disk priority: the
// check is background work that must never compete with someone watching.
type FFmpeg struct {
	FFprobe, FFmpeg string // "" = "ffprobe", "ffmpeg"
}

func (f FFmpeg) bin(name, def string) string {
	if name == "" {
		return def
	}
	return name
}

// Probe reads the streams and length, then samples the first audio stream's loudness.
func (f FFmpeg) Probe(ctx context.Context, path string) Probe {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	out, err := toolrun.Command(pctx, toolrun.Idle, f.bin(f.FFprobe, "ffprobe"),
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	cancel()
	if err != nil {
		return Probe{Err: fmt.Errorf("ffprobe: %w", err)}
	}
	p, err := ParseStreams(out)
	if err != nil {
		return Probe{Err: err}
	}
	if p.HasVideo && p.DurationSec > 0 {
		p.VideoMissingAt = f.videoGap(ctx, path, p.DurationSec)
	}
	if len(p.AudioLangs) == 0 || p.DurationSec <= 0 {
		return p
	}
	for _, at := range windowAt {
		if ctx.Err() != nil {
			break
		}
		p.Windows++
		start := p.DurationSec * at
		if start+windowSec > p.DurationSec {
			start = max(0, p.DurationSec-windowSec)
		}
		wctx, cancel := context.WithTimeout(ctx, windowTimeout)
		// volumedetect prints its result to stderr; the decoded audio goes nowhere.
		msg, _ := toolrun.Command(wctx, toolrun.Idle, f.bin(f.FFmpeg, "ffmpeg"), "-hide_banner", "-nostats",
			"-ss", strconv.FormatFloat(start, 'f', 1, 64), "-t", strconv.Itoa(windowSec), "-i", path,
			"-map", "0:a:0", "-af", "volumedetect", "-vn", "-sn", "-dn", "-f", "null", "-").CombinedOutput()
		cancel()
		if db, ok := ParseMaxVolume(msg); ok {
			p.Loudness = append(p.Loudness, db)
		}
	}
	return p
}

// videoGap returns the first sampled point (seconds) with no video packet, or 0 when the
// picture runs to the end. A window ffprobe couldn't read says nothing either way.
func (f FFmpeg) videoGap(ctx context.Context, path string, dur float64) float64 {
	for _, at := range videoAt {
		if ctx.Err() != nil {
			return 0
		}
		start := dur * at
		if start+windowSec > dur {
			start = max(0, dur-windowSec)
		}
		wctx, cancel := context.WithTimeout(ctx, probeTimeout)
		out, err := toolrun.Command(wctx, toolrun.Idle, f.bin(f.FFprobe, "ffprobe"), "-v", "error",
			"-select_streams", "v:0", "-read_intervals", strconv.FormatFloat(start, 'f', 1, 64)+"%"+strconv.FormatFloat(start+windowSec, 'f', 1, 64),
			"-show_entries", "packet=pts_time", "-of", "csv=p=0", path).Output()
		cancel()
		if err != nil {
			continue
		}
		if !HasPacketFrom(out, start) {
			return start
		}
	}
	return 0
}

// HasPacketFrom reports a packet time (one per line, ffprobe's csv) at or after from. A
// seek lands on the keyframe before it (hence an absolute end for the interval), so
// earlier packets alone mean the picture has already stopped.
func HasPacketFrom(out []byte, from float64) bool {
	for _, line := range strings.Split(string(out), "\n") {
		if t, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ",")), 64); err == nil && t >= from-1 {
			return true
		}
	}
	return false
}

// ParseStreams reads ffprobe's JSON: one language tag per audio stream (cover art and
// other attachments aren't streams worth counting) and the container's length.
func ParseStreams(out []byte) (Probe, error) {
	var r struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Tags      struct {
				Language string `json:"language"`
			} `json:"tags"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return Probe{}, fmt.Errorf("ffprobe output: %w", err)
	}
	var p Probe
	for _, s := range r.Streams {
		switch s.CodecType {
		case "audio":
			p.AudioLangs = append(p.AudioLangs, s.Tags.Language)
		case "video":
			p.HasVideo = true
		}
	}
	if !p.HasVideo && len(p.AudioLangs) == 0 {
		return Probe{}, errors.New("no audio or video streams")
	}
	if d, err := strconv.ParseFloat(r.Format.Duration, 64); err == nil {
		p.DurationSec = d
	}
	return p, nil
}

var maxVolumeRe = regexp.MustCompile(`max_volume:\s*(-?(?:[0-9]+(?:\.[0-9]*)?|inf))\s*dB`)

// ParseMaxVolume finds volumedetect's "max_volume: -91.0 dB" in ffmpeg's output. A window
// of pure digital silence reads "-inf"; ok=false when the line isn't there (nothing was
// decoded).
func ParseMaxVolume(out []byte) (float64, bool) {
	m := maxVolumeRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	if string(m[1]) == "-inf" {
		return -200, true
	}
	db, err := strconv.ParseFloat(string(m[1]), 64)
	return db, err == nil
}
