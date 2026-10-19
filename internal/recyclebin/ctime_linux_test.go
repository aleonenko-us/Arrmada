//go:build linux

package recyclebin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A file with another name (a library file hard-linked to a seeding download) keeps its
// old mtime in the bin, and when its sidecar couldn't be written the change time the move
// set is its age: it isn't purged at once as if it had been binned a month ago.
func TestEnforceAgesALinkedFileByItsChangeTime(t *testing.T) {
	svc, set, dir := newTestSvc(t)
	ctx := context.Background()
	_ = set.Set(ctx, keyRetention, "7")
	linked := writeFile(t, dir, "Artist", "01.flac", 100, 30) // mtime a month old, no sidecar
	if err := os.Link(linked, filepath.Join(t.TempDir(), "seeding.flac")); err != nil {
		t.Skip(err)
	}
	alone := writeFile(t, dir, "Other", "o.mkv", 100, 30)
	svc.Enforce(ctx)
	if gone(linked) {
		t.Error("the linked file was purged by its old mtime")
	}
	if !gone(alone) {
		t.Error("a single-name file past retention survived")
	}
}
