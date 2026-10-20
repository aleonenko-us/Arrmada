package quality

import "testing"

// MUS-14: a fresh install gets one sensible music profile, once; the seeding never
// overwrites or adds to an existing one, and a removed seeded profile isn't brought back.
// (The app won't delete a media's last profile, so the tests remove it directly: the
// seeding must hold even then.)
func TestEnsureDefaultMusicProfileSeedsOnce(t *testing.T) {
	s, db, ctx := storeService(t)
	created, err := s.EnsureDefaultMusicProfile(ctx)
	if err != nil || !created {
		t.Fatalf("first start: created=%v err=%v, want one profile", created, err)
	}
	list, err := s.ListStored(ctx, MediaMusic)
	if err != nil || len(list) != 1 {
		t.Fatalf("music profiles = %d (%v), want 1", len(list), err)
	}
	sp := list[0]
	if sp.Name != DefaultMusicPreset || sp.FormatScores["FLAC"] <= 0 || sp.FormatScores["MP3-128"] > 0 {
		t.Fatalf("seeded profile = %+v, want the %q preset", sp, DefaultMusicPreset)
	}
	if sp.UpgradesEnabled {
		t.Error("the seeded profile must not upgrade until the owner says so")
	}
	if FallbackMusicProfile().UpgradesEnabled {
		t.Error("the fallback profile must never upgrade")
	}
	for _, p := range MusicPresets() {
		if p.UpgradesEnabled {
			t.Errorf("preset %q starts with upgrades on; the owner turns them on", p.Name)
		}
	}
	if got := s.DefaultProfile(ctx, MediaMusic); got == "" {
		t.Fatal("the seeded profile should be the music default (no more silent 'Any quality')")
	}

	// A second start does nothing.
	if created, err := s.EnsureDefaultMusicProfile(ctx); err != nil || created {
		t.Fatalf("second start: created=%v err=%v, want nothing", created, err)
	}
	// Removed: never resurrected.
	mustExec(t, db, `DELETE FROM quality_profiles WHERE id = ?`, sp.ID)
	if created, err := s.EnsureDefaultMusicProfile(ctx); err != nil || created {
		t.Fatalf("after a delete: created=%v err=%v, want nothing", created, err)
	}
	if list, _ := s.ListStored(ctx, MediaMusic); len(list) != 0 {
		t.Fatalf("music profiles after a delete = %d, want 0", len(list))
	}
}

func TestEnsureDefaultMusicProfileKeepsExisting(t *testing.T) {
	s, db, ctx := storeService(t)
	mine, err := s.Create(ctx, StoredProfile{MediaType: MediaMusic, Name: "Mine", FormatScores: map[string]int{"MP3-320": 10}, MinFormatScore: 1})
	if err != nil {
		t.Fatal(err)
	}
	if created, err := s.EnsureDefaultMusicProfile(ctx); err != nil || created {
		t.Fatalf("with a profile: created=%v err=%v, want nothing", created, err)
	}
	list, _ := s.ListStored(ctx, MediaMusic)
	if len(list) != 1 || list[0].ID != mine.ID || list[0].Name != "Mine" || list[0].FormatScores["MP3-320"] != 10 {
		t.Fatalf("profiles = %+v, want only Mine, untouched", list)
	}
	// And it's marked: losing Mine later doesn't seed one.
	mustExec(t, db, `DELETE FROM quality_profiles WHERE id = ?`, mine.ID)
	if created, _ := s.EnsureDefaultMusicProfile(ctx); created {
		t.Fatal("seeded after the owner's only profile was removed")
	}
}
