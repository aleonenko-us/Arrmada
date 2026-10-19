package acoustid

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
)

// Fingerprinter answers the music matcher's second pass: which MusicBrainz recordings a
// file is, by its sound. A file is fingerprinted and looked up once per process (per path,
// size and modification time): an import that waits and retries every 30 seconds doesn't
// decode it again. A failure is remembered for a while too, so an AcoustID outage isn't
// asked about on every sweep.
type Fingerprinter struct {
	client      *Client
	fingerprint func(ctx context.Context, path string) (string, error)
	duration    func(ctx context.Context, path string) (int, error)
	available   func() bool
	now         func() time.Time

	mu    sync.Mutex
	cache map[cacheKey]cacheEntry
}

type cacheKey struct {
	path        string
	size, mtime int64
}

type cacheEntry struct {
	recs []string
	err  error
	at   time.Time
}

// cacheMax bounds the cache; past it, it starts again (a busy import of a few hundred
// files is far below it).
const cacheMax = 20000

// failureTTL is how long a failed fingerprint or lookup is remembered before trying again.
const failureTTL = 30 * time.Minute

// NewFingerprinter fingerprints with the bundled ffmpeg and measures with ffprobe.
func NewFingerprinter(c *Client) *Fingerprinter {
	return &Fingerprinter{
		client: c, fingerprint: mediainfo.Fingerprint, duration: probeSeconds, available: ffmpegFingerprints,
		now: time.Now, cache: map[cacheKey]cacheEntry{},
	}
}

func probeSeconds(ctx context.Context, path string) (int, error) {
	info, err := mediainfo.ProbeAudio(ctx, path)
	if err != nil {
		return 0, err
	}
	return int(math.Round(info.DurationSec)), nil
}

// Enabled reports whether the second pass can run: a key is set and ffmpeg can make
// fingerprints here.
func (f *Fingerprinter) Enabled() bool {
	return f != nil && f.client.Enabled() && f.available()
}

// ffmpegFingerprints reports whether the bundled ffmpeg has the chromaprint muxer (asked
// once per process).
func ffmpegFingerprints() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return mediainfo.FingerprintAvailable(ctx)
}

// Recordings fingerprints a file and returns the MusicBrainz recordings AcoustID names for
// it, best match first. None (and no error) when AcoustID doesn't know the sound.
func (f *Fingerprinter) Recordings(ctx context.Context, path string) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	key := cacheKey{path, fi.Size(), fi.ModTime().UnixNano()}
	f.mu.Lock()
	e, ok := f.cache[key]
	f.mu.Unlock()
	if ok && (e.err == nil || f.now().Sub(e.at) < failureTTL) {
		return e.recs, e.err
	}
	recs, err := f.lookup(ctx, path)
	if ctx.Err() != nil {
		return nil, ctx.Err() // cancelled: nothing learned, nothing remembered
	}
	f.mu.Lock()
	if len(f.cache) >= cacheMax {
		f.cache = map[cacheKey]cacheEntry{}
	}
	f.cache[key] = cacheEntry{recs: recs, err: err, at: f.now()}
	f.mu.Unlock()
	return recs, err
}

func (f *Fingerprinter) lookup(ctx context.Context, path string) ([]string, error) {
	fp, err := f.fingerprint(ctx, path)
	if err != nil {
		return nil, err
	}
	dur, err := f.duration(ctx, path)
	if err != nil {
		return nil, err
	}
	if dur <= 0 {
		return nil, errors.New("couldn't tell how long the file is")
	}
	matches, err := f.client.Lookup(ctx, fp, dur, "")
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		for _, r := range m.Recordings {
			if k := strings.ToLower(r); !seen[k] {
				seen[k] = true
				out = append(out, r)
			}
		}
	}
	return out, nil
}
