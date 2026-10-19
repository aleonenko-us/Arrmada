package recyclebin

import (
	"context"
	"errors"
	"testing"
)

// A file an interrupted music import holds (to put back if its new copy never lands) is
// never aged out, capped, emptied or deleted; the rest of the bin is.
func TestHeldItemsStay(t *testing.T) {
	svc, set, dir := newTestSvc(t)
	ctx := context.Background()
	_ = set.Set(ctx, keyRetention, "7")
	held := writeFile(t, dir, "Artist/Album", "01.flac", 100, 30)
	other := writeFile(t, dir, "Other", "o.mkv", 100, 30)
	svc.SetHeld(func() map[string]bool { return map[string]bool{held: true} })

	svc.Enforce(ctx)
	if gone(held) || !gone(other) {
		t.Fatalf("after Enforce: held gone=%v, other gone=%v; want only the other purged", gone(held), gone(other))
	}
	var id string
	for _, it := range svc.List(ctx) {
		if it.Name == "01.flac" {
			id = it.ID
		}
	}
	if id == "" {
		t.Fatal("the held file isn't listed")
	}
	if err := svc.DeleteItem(ctx, id); !errors.Is(err, ErrHeld) {
		t.Errorf("DeleteItem = %v, want ErrHeld", err)
	}
	writeFile(t, dir, "Other", "p.mkv", 100, 1)
	if _, err := svc.Empty(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if gone(held) {
		t.Error("Empty removed the held file")
	}
	if n := len(svc.List(ctx)); n != 1 {
		t.Errorf("after Empty: %d items, want only the held one", n)
	}

	svc.SetHeld(func() map[string]bool { return nil }) // the job settled
	if err := svc.DeleteItem(ctx, id); err != nil {
		t.Errorf("DeleteItem once released: %v", err)
	}
}
