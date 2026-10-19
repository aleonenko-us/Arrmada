package mediainfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// Fingerprints (MUS-32): a Chromaprint fingerprint of an audio file, which AcoustID turns
// into MusicBrainz recording ids. The bundled ffmpeg (jellyfin-ffmpeg, built with
// --enable-chromaprint) has the chromaprint muxer, so no separate fpcalc is needed. Its
// default algorithm is fpcalc's (TEST2), and its base64 output is the compressed,
// URL-safe form AcoustID's lookup takes.

// fingerprintSeconds is how much of a file is fingerprinted: AcoustID's own tools use the
// first two minutes.
const fingerprintSeconds = 120

// ErrNoChromaprint means the ffmpeg here has no chromaprint muxer.
var ErrNoChromaprint = errors.New("this ffmpeg can't make audio fingerprints (no chromaprint muxer)")

// fingerprintArgs is the one ffmpeg command line for a fingerprint: decode the first two
// minutes of the first audio stream and write the fingerprint to stdout.
func fingerprintArgs(path string) []string {
	return []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-i", path, "-map", "0:a:0",
		"-t", fmt.Sprint(fingerprintSeconds), "-f", "chromaprint", "-fp_format", "base64", "-"}
}

var (
	chromaOnce sync.Once
	chromaOK   bool
)

// FingerprintAvailable reports whether ffmpeg can make fingerprints here (asked once).
func FingerprintAvailable(ctx context.Context) bool {
	chromaOnce.Do(func() {
		out, err := toolrun.Command(ctx, toolrun.Normal, "ffmpeg", "-hide_banner", "-muxers").Output()
		chromaOK = err == nil && bytes.Contains(out, []byte(" chromaprint "))
	})
	return chromaOK
}

// Fingerprint returns the Chromaprint fingerprint of an audio file's first two minutes,
// decoded by ffmpeg at idle priority. It only reads the file.
func Fingerprint(ctx context.Context, path string) (string, error) {
	if !FingerprintAvailable(ctx) {
		return "", ErrNoChromaprint
	}
	cmd := toolrun.Command(ctx, toolrun.Idle, "ffmpeg", fingerprintArgs(path)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return "", fmt.Errorf("fingerprint: %w: %s", err, msg)
	}
	fp := strings.TrimSpace(string(out))
	if fp == "" {
		return "", errors.New("fingerprint: ffmpeg wrote nothing (no audio?)")
	}
	return fp, nil
}
