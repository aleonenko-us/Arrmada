package audiobook

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The merged file carries the book's own tags — Arrmada's record first, the first
// source's where the record says nothing — and the first source's cover; a cover the
// container refuses costs one retry without it, never the merge.
func TestMergeArgsCarryTagsAndCover(t *testing.T) {
	src := map[string]string{"title": "Chapter 01", "album": "Tone Book (Unabridged)", "artist": "a. sine",
		"genre": "Speech", "comment": "ripped", "composer": "Tagged Reader", "track": "1/20"}
	tags := mergeTags(src, MergeOptions{Title: "The Tone Book", Author: "A. Sine", Series: "The Sine Cycle",
		SeriesPosition: 1.5, Year: 2021, Narrator: "B. Square"})
	want := []string{"title=The Tone Book", "artist=A. Sine", "album_artist=A. Sine", "album=The Tone Book",
		"composer=B. Square", "grouping=The Sine Cycle #1.5", "genre=Audiobook", "date=2021", "comment=ripped", "media_type=2"}
	if strings.Join(tags, "|") != strings.Join(want, "|") {
		t.Errorf("tags\n got %q\nwant %q", tags, want)
	}
	// With nothing from the library, the source's own album names the book and its
	// composer stays the narrator; a chapter's track number never carries over.
	bare := strings.Join(mergeTags(src, MergeOptions{}), "|")
	for _, w := range []string{"title=Tone Book (Unabridged)", "artist=a. sine", "album_artist=a. sine", "composer=Tagged Reader", "genre=Audiobook"} {
		if !strings.Contains(bare, w) {
			t.Errorf("bare tags %q missing %q", bare, w)
		}
	}
	if strings.Contains(bare, "track=") {
		t.Errorf("a chapter's track number carried over: %q", bare)
	}

	out := filepath.Join(t.TempDir(), ".Book.m4b.merging") // a retry clears it first
	in := mergeInputs{list: "list.txt", chapters: "chapters.ffmeta", first: "/books/01.mp3",
		infos: []audioInfo{{Codec: "mp3", BitrateBPS: 128_000}}, tags: tags, out: out}
	with := strings.Join(mergeArgs(in, true), " ")
	for _, w := range []string{"-i /books/01.mp3", "-map_metadata -1", "-map_chapters 1", "-map 0:a", "-map 2:v:0?",
		"-c:v copy", "-disposition:v:0 attached_pic", "-metadata title=The Tone Book", "-metadata media_type=2", "-f mp4 " + out} {
		if !strings.Contains(with, w) {
			t.Errorf("args with cover %q missing %q", with, w)
		}
	}
	without := strings.Join(mergeArgs(in, false), " ")
	for _, gone := range []string{"/books/01.mp3", "2:v", "attached_pic", "-c:v"} {
		if strings.Contains(without, gone) {
			t.Errorf("args without cover %q still have %q", without, gone)
		}
	}

	// A cover the MP4 container rejects: tried once more without it.
	var runs []string
	run := func(_ context.Context, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		runs = append(runs, joined)
		if strings.Contains(joined, "2:v:0?") {
			return []byte("Could not find tag for codec bmp"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if err := runMerge(context.Background(), in, true, run); err != nil || len(runs) != 2 || strings.Contains(runs[1], "2:v") {
		t.Fatalf("cover retry: err %v, runs %q", err, runs)
	}
	// No cover to blame: a failure is final, not run twice.
	runs = nil
	fail := func(_ context.Context, args []string) ([]byte, error) {
		runs = append(runs, strings.Join(args, " "))
		return []byte("No space left on device"), errors.New("exit status 1")
	}
	if err := runMerge(context.Background(), in, false, fail); err == nil || len(runs) != 1 || !strings.Contains(err.Error(), "No space left") {
		t.Fatalf("failure without a cover: err %v, runs %d", err, len(runs))
	}
	// A cover retry that fails too reports both.
	runs = nil
	if err := runMerge(context.Background(), in, true, fail); err == nil || len(runs) != 2 || !strings.Contains(err.Error(), "without the cover") {
		t.Fatalf("failed retry: err %v, runs %d", err, len(runs))
	}
}
