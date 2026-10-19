package music

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// Album covers for the house style (MUS-33): the Cover Art Archive's front image, the
// 500 px size embedded in every track and the 1200 px size as cover.jpg beside them. MUS-23
// will put an on-disk art cache in front of this; until then each import fetches what it
// needs straight from CAA (keyless, keyed by MBID). A failed fetch only means no cover: it
// never stops an import.

// CoverSource fetches an album's front cover at a size (500 or 1200; 0 = the original).
// A cover that doesn't exist answers ErrNoCoverArt.
type CoverSource interface {
	Front(ctx context.Context, releaseMBID, groupMBID string, size int) ([]byte, error)
}

// ErrNoCoverArt means the release (and its release group) have no front cover.
var ErrNoCoverArt = errors.New("no cover art")

// maxCoverBytes bounds one download: CAA's thumbnails are far smaller, and an original
// scan bigger than this isn't put into every track.
const maxCoverBytes = 8 << 20

// CAA fetches covers from the Cover Art Archive.
type CAA struct {
	Base   string // https://coverartarchive.org; tests point it at a local server
	Client *http.Client
}

// NewCAA is the Cover Art Archive client the app uses.
func NewCAA() *CAA {
	return &CAA{Base: "https://coverartarchive.org", Client: httpua.Client(30 * time.Second)}
}

// Front fetches the release's front cover, falling back to its release group's. Only a
// JPEG or PNG under maxCoverBytes is returned.
func (c *CAA) Front(ctx context.Context, releaseMBID, groupMBID string, size int) ([]byte, error) {
	suffix := "front"
	if size > 0 {
		suffix = fmt.Sprintf("front-%d", size)
	}
	var lastErr error = ErrNoCoverArt
	for _, p := range []struct{ kind, id string }{{"release", mbid(releaseMBID)}, {"release-group", mbid(groupMBID)}} {
		if p.id == "" {
			continue
		}
		b, err := c.get(ctx, c.Base+"/"+p.kind+"/"+p.id+"/"+suffix)
		if err == nil {
			return b, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *CAA) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoCoverArt
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover art archive: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxCoverBytes {
		return nil, errors.New("cover art archive: the image is too big to embed")
	}
	if !bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}) && !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, errors.New("cover art archive: not a JPEG or PNG image")
	}
	return b, nil
}

// IsJPEG reports whether b is a JPEG image (cover.jpg must be one).
func IsJPEG(b []byte) bool { return bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}) }
