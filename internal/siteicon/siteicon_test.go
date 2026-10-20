package siteicon

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestUnknownSlugIsRefused(t *testing.T) {
	if _, _, err := New(t.TempDir()).Get(context.Background(), "../../etc/passwd"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("got %v, want ErrUnknown", err)
	}
}

func TestSniffTakesOnlyRasterImages(t *testing.T) {
	if ct, err := sniff([]byte{0, 0, 1, 0, 1, 0}); err != nil || ct != "image/x-icon" {
		t.Fatalf("ico: %q %v", ct, err)
	}
	for _, b := range [][]byte{nil, []byte("<html>Just a moment...</html>"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`)} {
		if _, err := sniff(b); err == nil {
			t.Fatalf("accepted %q", b)
		}
	}
}

// SITEICON_LIVE=1 fetches every listed icon for real and says which ones came back.
func TestLiveIcons(t *testing.T) {
	if os.Getenv("SITEICON_LIVE") == "" {
		t.Skip("set SITEICON_LIVE=1 to fetch the real icons")
	}
	s := New(t.TempDir())
	for slug := range Hosts {
		b, ct, err := s.Get(context.Background(), slug)
		t.Logf("%-16s %-14s %6d bytes %v", slug, ct, len(b), err)
	}
}
