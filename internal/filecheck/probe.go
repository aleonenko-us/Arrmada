package filecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
	video := false
	for _, s := range r.Streams {
		switch s.CodecType {
		case "audio":
			p.AudioLangs = append(p.AudioLangs, s.Tags.Language)
		case "video":
			video = true
		}
	}
	if !video && len(p.AudioLangs) == 0 {
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
