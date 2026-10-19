package health

import (
	"context"
	"testing"
)

// MUS-15: the music file check's refusal shows as a warning that links to the library
// folders, read from memory only.
func TestMusicFilesCheck(t *testing.T) {
	msg := ""
	c := MusicFilesCheck(func() string { return msg })
	if got := c.Run(context.Background()); len(got) != 0 {
		t.Fatalf("all well: %+v", got)
	}
	msg = "The music folder /mnt/user/Music looks unmounted"
	got := c.Run(context.Background())
	if len(got) != 1 || got[0].Level != LevelWarning || got[0].Message != msg || got[0].Fix != FixLibraryFolders {
		t.Fatalf("unmounted: %+v", got)
	}
}
