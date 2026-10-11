package library

import (
	"context"
	"testing"
)

// The imports list (History's "Files imported") shows files that were placed. A
// dismissed review, a pack that placed nothing, or a series/book import recorded only so
// the download isn't imported twice has no target path, and isn't an import.
func TestRecentLeavesOutRowsWithoutAFile(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, t.TempDir(), nil)
	if err := m.repo.record(ctx, ImportRecord{Hash: "placed", TargetPath: "/lib/Dune (2021)/Dune.mkv", Title: "Dune"}, AttachStateAttached); err != nil {
		t.Fatal(err)
	}
	if _, err := m.repo.db.ExecContext(ctx,
		`INSERT INTO imports (download_hash, source_path, target_path, title, size_bytes) VALUES ('dismissed', '', '', 'Junk.Release', 1)`); err != nil {
		t.Fatal(err)
	}
	got, err := m.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hash != "placed" {
		t.Fatalf("Recent = %+v, want only the placed file", got)
	}
	// The hash is still recorded, so the download isn't imported again.
	if hs, _ := m.ImportedHashes(ctx); !hs["dismissed"] {
		t.Error("a recorded-only row must still count as imported")
	}
}
