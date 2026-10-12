package books

import (
	"errors"
	"testing"
)

func groupIDs(g DupGroup) []int64 {
	var out []int64
	for _, b := range g.Books {
		out = append(out, b.ID)
	}
	return out
}

// The review lists the same novel under two catalogue keys, never prefix siblings or
// subtitle-only matches, a similar author in a weaker tier, and flagged pairs; it drops
// pairs marked as different, groups transitively and suggests the fullest row.
func TestPossibleDuplicates(t *testing.T) {
	f := newMergeFixture(t)
	ctx := f.ctx
	olRow := f.book("OL1W", "The Way of Kings")
	hcRow := f.book("hc:1", "The Way of Kings (The Stormlight Archive, #1)")
	f.edition(hcRow.ID, KindAudiobook, "/lib/a/wok")
	thrawn := f.book("OL2W", "Thrawn")
	f.book("OL3W", "Thrawn: Alliances")
	// The same title, a co-author added: the weaker tier.
	f.exec(`UPDATE books SET author = 'Brandon Sanderson' WHERE id = ?`, thrawn.ID)
	co, err := f.s.repo.Create(ctx, Book{OLKey: "OL4W", Title: "Thrawn", Author: "Brandon Sanderson, Timothy Zahn"})
	if err != nil {
		t.Fatal(err)
	}
	// A flagged pair the titles alone wouldn't find, joined to the first pair through hcRow.
	other := f.book("hc:9", "Stormlight 1")
	f.s.flagDuplicate(ctx, hcRow.ID, other.ID, DupReasonHardcover)
	// And a pair marked as different books.
	elantris1 := f.book("OL5W", "Elantris")
	elantris2 := f.book("hc:5", "Elantris")
	if err := f.s.IgnoreDuplicates(ctx, []int64{elantris2.ID, elantris1.ID}); err != nil {
		t.Fatal(err)
	}

	groups, err := f.s.PossibleDuplicates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	wok := groups[0]
	if ids := groupIDs(wok); len(ids) != 3 || ids[0] != olRow.ID || ids[1] != hcRow.ID || ids[2] != other.ID {
		t.Fatalf("first group = %v", ids)
	}
	if wok.SuggestedKeepID != hcRow.ID {
		t.Fatalf("suggested %d, want the row with the audiobook (%d)", wok.SuggestedKeepID, hcRow.ID)
	}
	if len(wok.Reasons) != 2 || wok.Reasons[0] != DupReasonSameBook || wok.Reasons[1] != DupReasonHardcover {
		t.Fatalf("reasons = %v", wok.Reasons)
	}
	th := groups[1]
	if ids := groupIDs(th); len(ids) != 2 || ids[0] != thrawn.ID || ids[1] != co.ID || th.Reasons[0] != DupReasonSimilar {
		t.Fatalf("second group = %v %v (Thrawn: Alliances must never join)", ids, th.Reasons)
	}
	if th.SuggestedKeepID != thrawn.ID {
		t.Fatalf("tie: suggested %d, want the oldest", th.SuggestedKeepID)
	}

	// Merging is only for books in one group.
	if _, err := f.s.MergeDuplicates(ctx, olRow.ID, []int64{co.ID}, MergeOptions{}); !errors.Is(err, ErrNotDuplicates) {
		t.Fatalf("merge across groups: %v", err)
	}
	if _, err := f.s.MergeDuplicates(ctx, olRow.ID, []int64{olRow.ID}, MergeOptions{}); !errors.Is(err, ErrNotDuplicates) {
		t.Fatalf("merge into itself: %v", err)
	}
	if _, err := f.s.MergeDuplicates(ctx, elantris1.ID, []int64{elantris2.ID}, MergeOptions{}); !errors.Is(err, ErrNotDuplicates) {
		t.Fatalf("merge of an ignored pair: %v", err)
	}
	res, err := f.s.MergeDuplicates(ctx, hcRow.ID, []int64{olRow.ID, other.ID}, MergeOptions{})
	if err != nil || len(res) != 2 {
		t.Fatalf("merge = %+v %v", res, err)
	}
	groups, _ = f.s.PossibleDuplicates(ctx)
	if len(groups) != 1 || groups[0].Books[0].ID != thrawn.ID {
		t.Fatalf("after merge: %+v", groups)
	}
}

// "Not the same" survives a merge: the merged book is still not the other one.
func TestIgnoredPairSurvivesMerge(t *testing.T) {
	f := newMergeFixture(t)
	a := f.book("OL1W", "Emma")
	b := f.book("hc:1", "Emma")
	c := f.book("hc:2", "Emma")
	if err := f.s.IgnoreDuplicates(f.ctx, []int64{b.ID, c.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Merge(f.ctx, a.ID, b.ID, MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM book_dupe_reviews WHERE a_id = ? AND b_id = ? AND status = 'ignored'`, a.ID, c.ID); n != 1 {
		t.Fatalf("ignored pair carried over: %d", n)
	}
	groups, _ := f.s.PossibleDuplicates(f.ctx)
	if len(groups) != 0 {
		t.Fatalf("groups = %+v", groups)
	}
	if err := f.s.IgnoreDuplicates(f.ctx, []int64{a.ID, a.ID}); !errors.Is(err, ErrPickTwo) {
		t.Fatalf("one book: %v", err)
	}
	if err := f.s.IgnoreDuplicates(f.ctx, []int64{a.ID, 9999}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing book: %v", err)
	}
}

// Earlier merges list book merges — old automatic folds with the lost book's title and
// key, and reviewed ones — but not an audiobook's chapter files being combined.
func TestPastMerges(t *testing.T) {
	f := newMergeFixture(t)
	keep := f.book("OL1W", "Mistborn: The Final Empire")
	f.s.repo.AddEvent(f.ctx, keep.ID, "merged", `Merged duplicate entry "Mistborn: Secret History" (OL9W)`)
	f.s.repo.AddEvent(f.ctx, keep.ID, "merged", "Merged 12 chapter files into Mistborn.m4b. The original files went to the recycle bin.")
	drop := f.book("hc:1", "Mistborn: The Final Empire")
	if _, err := f.s.Merge(f.ctx, keep.ID, drop.ID, MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	past, err := f.s.PastMerges(f.ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 2 {
		t.Fatalf("past = %+v", past)
	}
	if past[0].Automatic || past[0].LostTitle != "Mistborn: The Final Empire" || past[0].LostKey != "hc:1" {
		t.Fatalf("reviewed merge = %+v", past[0])
	}
	if !past[1].Automatic || past[1].LostTitle != "Mistborn: Secret History" || past[1].LostKey != "OL9W" || past[1].KeeperTitle != keep.Title {
		t.Fatalf("automatic merge = %+v", past[1])
	}
}
