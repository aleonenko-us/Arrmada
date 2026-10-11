package health

import (
	"context"
	"testing"
)

// The missing-file check's unmounted verdict becomes one warning linking to the library
// folders, and clears when the next sweep is happy.
func TestMovieFilesCheck(t *testing.T) {
	msg := ""
	c := MovieFilesCheck(func() string { return msg })
	if got := c.Run(context.Background()); len(got) != 0 {
		t.Fatalf("all well: %+v", got)
	}
	msg = "The movies folder /mnt/user/Movies looks unmounted"
	got := c.Run(context.Background())
	if len(got) != 1 || got[0].Level != LevelWarning || got[0].Message != msg || got[0].Fix != FixLibraryFolders {
		t.Fatalf("unmounted: %+v", got)
	}
}
