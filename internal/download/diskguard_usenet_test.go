package download

import (
	"testing"
)

// The guard holds usenet jobs by their key, on the usenet client alone: a job still
// downloading is paused and later resumed; one being unpacked (it can't be paused), one
// SABnzbd paused itself (its own free-space pause — not the guard's to undo) and a torrent
// key are each left to their own client.
func TestGuardHoldsUsenetJobsByKey(t *testing.T) {
	g, qbit, _, ctx := guardFixture(t, []Item{{Hash: "aaa", State: "downloading"}})
	sab := &fakeClient{items: []Item{
		{Hash: "sab-down", State: "downloading", RawState: "Downloading"},
		{Hash: "sab-queued", State: "downloading", RawState: "Queued"},
		{Hash: "sab-unpack", State: PhaseUnpacking, RawState: "Extracting", Progress: 1},
		{Hash: "sab-selfpaused", State: "paused", RawState: "Paused"},
	}}
	g.svc.registry.impls[KindSABnzbd] = sab
	if _, err := g.svc.repo.Create(ctx, Client{Name: "sab", Kind: KindSABnzbd, URL: "http://sab", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	g.pauseActive(ctx, nil, 92, 85, "/dl")
	if !sameSet(sab.paused, "sab-down", "sab-queued") {
		t.Errorf("usenet client paused %v, want the two jobs still downloading", sab.paused)
	}
	if !sameSet(qbit.paused, "aaa") {
		t.Errorf("torrent client paused %v, want only its own torrent", qbit.paused)
	}
	if held := g.Held(ctx); !held["sab-down"] || !held["sab-queued"] || held["sab-unpack"] || held["sab-selfpaused"] {
		t.Errorf("held = %v", held)
	}

	g.resume(ctx, g.held(ctx))
	if !sameSet(sab.resumed, "sab-down", "sab-queued") {
		t.Errorf("usenet client resumed %v, want only what the guard paused", sab.resumed)
	}
	if !sameSet(qbit.resumed, "aaa") {
		t.Errorf("torrent client resumed %v", qbit.resumed)
	}
}

func sameSet(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}
