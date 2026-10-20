package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsExtrasFolder(t *testing.T) {
	for _, n := range []string{"Extras", "featurettes", "Behind The Scenes", "Deleted Scenes", "Interviews", "Scenes", "Shorts", "TRAILERS", "Other", " Extras "} {
		if !IsExtrasFolder(n) {
			t.Errorf("%q isn't an extras folder", n)
		}
	}
	for _, n := range []string{"Season 1", "S01", "Specials", "Extra Ordinary", "Others", "Bonus Round"} {
		if IsExtrasFolder(n) {
			t.Errorf("%q is an extras folder", n)
		}
	}
}

// A link that may bring another show's files in: to a folder, to a video, or one that
// can't be followed. A link to a poster or subtitle doesn't.
func TestLinkMayHoldVideo(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, "targets", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	folder := filepath.Join(dir, "targets", "Season 1")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		link, target string
		want         bool
	}{
		{"folder", folder, true},
		{"ep.mkv", write("ep.mkv"), true},
		{"renamed", write("other.mp4"), true},
		{"broken", filepath.Join(dir, "nowhere"), true},
		{"poster.jpg", write("poster.jpg"), false},
		{"show.srt", write("show.srt"), false},
	} {
		p := filepath.Join(dir, c.link)
		if err := os.Symlink(c.target, p); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
		if got := LinkMayHoldVideo(p); got != c.want {
			t.Errorf("LinkMayHoldVideo(%s -> %s) = %v, want %v", c.link, c.target, got, c.want)
		}
	}
}
