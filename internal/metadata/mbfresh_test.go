package metadata

import (
	"context"
	"testing"
	"time"
)

// P7: a release group that existed when last checked and has since been deleted reads as
// not found to a caller asking "does it still exist?" (WithMBFreshNotFound) once its
// cached answer is stale and MusicBrainz says 404 now. Everyone else keeps getting the
// cached answer, and a lookup MusicBrainz couldn't answer still serves it ("unknown").
func TestMBFreshNotFoundForDepartureChecks(t *testing.T) {
	m, f, now := cachedMB(t)
	f.mu.Lock()
	f.bodies["/release-group/rg-x"] = `{"id":"rg-x","title":"Gone Soon","primary-type":"Album"}`
	f.mu.Unlock()
	check := WithMBFreshNotFound(WithMBRevalidate(context.Background()))
	if rg, err := m.ReleaseGroup(check, "rg-x"); err != nil || rg.MBID != "rg-x" {
		t.Fatalf("seed: %+v %v", rg, err)
	}
	// Deleted on MusicBrainz; still fresh in the cache: the cached answer, no call.
	f.mu.Lock()
	delete(f.bodies, "/release-group/rg-x")
	f.mu.Unlock()
	before := f.requests.Load()
	if _, err := m.ReleaseGroup(check, "rg-x"); err != nil || f.requests.Load() != before {
		t.Fatalf("fresh cached answer: %v, %d calls", err, f.requests.Load()-before)
	}
	*now = now.Add(8 * 24 * time.Hour)
	// MusicBrainz down: unknown, so the cached answer.
	f.set("500")
	if _, err := m.ReleaseGroup(check, "rg-x"); err != nil {
		t.Errorf("MusicBrainz down: %v, want the cached answer", err)
	}
	// MusicBrainz answers 404 now: gone, for the departure check.
	f.set("")
	if _, err := m.ReleaseGroup(check, "rg-x"); !IsNotFound(err) {
		t.Errorf("fresh 404: %v, want not found", err)
	}
	// Every other caller still gets the kept answer.
	if rg, err := m.ReleaseGroup(context.Background(), "rg-x"); err != nil || rg.Title != "Gone Soon" {
		t.Errorf("an ordinary lookup after the 404 = %+v %v, want the cached answer kept", rg, err)
	}
	// And a revalidating caller that didn't ask "does it exist" isn't told it's gone.
	*now = now.Add(8 * 24 * time.Hour)
	if _, err := m.ReleaseGroup(WithMBRevalidate(context.Background()), "rg-x"); err != nil {
		t.Errorf("revalidate without the flag: %v, want the cached answer", err)
	}
}
