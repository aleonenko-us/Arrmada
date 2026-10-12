package books

import "testing"

// A narrator typed for the dropped book's audiobook goes where its file goes: onto the
// kept book when that had no audiobook, onto the "Merged copy" version when it had one.
// The dropped book's own versions keep theirs.
func TestMergeCarriesTheNarrator(t *testing.T) {
	t.Run("moved audiobook", func(t *testing.T) {
		f := newMergeFixture(t)
		keep := f.book("OL1W", "Elantris")
		drop := f.book("hc:7", "Elantris")
		f.edition(drop.ID, KindAudiobook, "/lib/a/elantris")
		if err := f.s.SetNarrator(f.ctx, drop.ID, "Jack Garrett"); err != nil {
			t.Fatal(err)
		}
		res, err := f.s.Merge(f.ctx, keep.ID, drop.ID, MergeOptions{})
		if err != nil || !res.MovedAudiobook {
			t.Fatalf("merge: %+v, %v", res, err)
		}
		if got, _ := f.s.Get(f.ctx, keep.ID); got.AudiobookNarrator != "Jack Garrett" {
			t.Fatalf("kept book's narrator = %q", got.AudiobookNarrator)
		}
	})
	t.Run("merged copy", func(t *testing.T) {
		f := newMergeFixture(t)
		keep := f.book("OL1W", "Warbreaker")
		drop := f.book("hc:8", "Warbreaker")
		f.edition(keep.ID, KindAudiobook, "/lib/a/warbreaker")
		f.edition(drop.ID, KindAudiobook, "/lib/a/warbreaker-2")
		if err := f.s.SetNarrator(f.ctx, keep.ID, "Alyssa Bresnahan"); err != nil {
			t.Fatal(err)
		}
		if err := f.s.SetNarrator(f.ctx, drop.ID, "James Yaegashi"); err != nil {
			t.Fatal(err)
		}
		v, err := f.s.AddAudioVersion(f.ctx, drop.ID, "Full cast", nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.SetAudioVersionNarrator(f.ctx, drop.ID, v.ID, "GraphicAudio cast"); err != nil {
			t.Fatal(err)
		}
		res, err := f.s.Merge(f.ctx, keep.ID, drop.ID, MergeOptions{})
		if err != nil || res.MergedCopyID == 0 {
			t.Fatalf("merge: %+v, %v", res, err)
		}
		got, _ := f.s.Get(f.ctx, keep.ID)
		if got.AudiobookNarrator != "Alyssa Bresnahan" {
			t.Fatalf("kept book's own narrator changed to %q", got.AudiobookNarrator)
		}
		copyV, err := f.s.GetAudioVersion(f.ctx, keep.ID, res.MergedCopyID)
		if err != nil || copyV.Narrator != "James Yaegashi" {
			t.Fatalf("merged copy = %+v, %v; want the dropped audiobook's narrator", copyV, err)
		}
		moved, err := f.s.GetAudioVersion(f.ctx, keep.ID, v.ID)
		if err != nil || moved.Narrator != "GraphicAudio cast" {
			t.Fatalf("moved version = %+v, %v", moved, err)
		}
	})
}
