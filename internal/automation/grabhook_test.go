package automation

import (
	"context"
	"testing"
)

func TestGrabbedHook(t *testing.T) {
	c := &Coordinator{}
	c.grabbed(context.Background(), "movie", 3) // no listener: nothing happens

	var got []string
	c.SetGrabbed(func(_ context.Context, mediaType string, itemID int64) {
		got = append(got, mediaType)
		if itemID != 3 {
			t.Errorf("item id = %d, want 3", itemID)
		}
	})
	c.grabbed(context.Background(), "movie", 3)
	c.grabbed(context.Background(), "series", 0) // no item: not worth a nudge
	if len(got) != 1 || got[0] != "movie" {
		t.Fatalf("listener heard %v, want [movie]", got)
	}
	c.SetGrabbed(nil)
	c.grabbed(context.Background(), "movie", 3)
	if len(got) != 1 {
		t.Fatal("a removed listener was still called")
	}
}
