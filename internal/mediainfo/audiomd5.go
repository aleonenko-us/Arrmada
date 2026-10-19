package mediainfo

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// ErrNoFFmpeg is returned when ffmpeg isn't installed.
var ErrNoFFmpeg = errors.New("ffmpeg isn't installed")

// audioMD5Timeout bounds one decode; a track decodes in a second or two.
const audioMD5Timeout = 5 * time.Minute

// AudioMD5 decodes a file's first audio stream and returns the MD5 of the samples
// (ffmpeg's md5 muxer). Two files with the same audio answer the same whatever their
// tags, so the music import uses it to check a tag write left the sound alone (MUS-33).
// It runs at idle priority through the tool runner and only reads the file.
func AudioMD5(ctx context.Context, path string) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", ErrNoFFmpeg
	}
	ctx, cancel := context.WithTimeout(ctx, audioMD5Timeout)
	defer cancel()
	out, err := toolrun.Command(ctx, toolrun.Idle, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", path, "-map", "0:a:0", "-f", "md5", "-").Output()
	if err != nil {
		return "", err
	}
	sum := strings.TrimSpace(string(out))
	if !strings.HasPrefix(sum, "MD5=") {
		return "", errors.New("ffmpeg gave no audio checksum")
	}
	return sum, nil
}
