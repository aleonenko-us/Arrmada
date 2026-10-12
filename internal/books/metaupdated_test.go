package books

import (
	"context"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// meta_updated_at moves when a book's details change, through whichever call changed
// them, and stays put for bookkeeping columns or a write that changes nothing.
func TestMetaUpdatedTriggerFiresOnlyOnMetadataColumns(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	svc := NewService(st.DB(), nil, slog.Default())
	added, _ := svc.AddWorks(ctx, []metadata.BookResult{{Key: "OL1W", Title: "Dune", Author: "Frank Herbert"}}, "", true)
	if len(added) != 1 {
		t.Fatalf("added %d", len(added))
	}
	id := added[0].ID
	stamp := func() int64 {
		t.Helper()
		b, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return b.MetaUpdatedAt
	}
	if s := stamp(); s != 0 {
		t.Fatalf("a new book starts at %d, want 0 (its added time stands)", s)
	}

	svc.RecordSearchMiss(ctx, id)
	svc.ResetSearchMisses(ctx, id)
	if err := svc.SetMonitored(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if s := stamp(); s != 0 {
		t.Fatalf("search bookkeeping and monitoring moved it to %d", s)
	}

	if err := svc.SetSeries(ctx, id, "Dune", 1); err != nil {
		t.Fatal(err)
	}
	s1 := stamp()
	if s1 == 0 {
		t.Fatal("a series learned didn't move it")
	}
	if err := svc.SetSeries(ctx, id, "Dune", 1); err != nil {
		t.Fatal(err)
	}
	if s := stamp(); s != s1 {
		t.Fatal("writing the same series again moved it")
	}
	if err := svc.SetCover(ctx, id, "https://covers.example/dune.jpg"); err != nil {
		t.Fatal(err)
	}
	s2 := stamp()
	if s2 <= s1 {
		t.Fatalf("a cover change: %d, want more than %d", s2, s1)
	}
	if err := svc.TouchMeta(ctx, id); err != nil {
		t.Fatal(err)
	}
	if s := stamp(); s <= s2 {
		t.Fatalf("TouchMeta: %d, want more than %d", s, s2)
	}

	// A version's own name moves its book too.
	v, err := svc.AddAudioVersion(ctx, id, "Full cast", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	s3 := stamp()
	if _, err := svc.UpdateAudioVersion(ctx, id, v.ID, "GraphicAudio", nil, true); err != nil {
		t.Fatal(err)
	}
	if s := stamp(); s <= s3 {
		t.Fatal("renaming a version didn't move its book")
	}
}
