package quality

import (
	"context"
	"fmt"
)

// The seeded music profile (MUS-14). With no music profile at all, every artist ran under
// FallbackMusicProfile, "Any quality": MP3-128 accepted, silently. A fresh install now gets
// one sensible profile instead, made once from the preset (so the ladder isn't written out
// twice). The settings flag records that the seeding is done, so a profile the owner later
// deletes is never brought back, and an install that already has a music profile is only
// marked, never given a second one.

// KeyMusicDefaultSeeded marks the music profile seeding as done.
const KeyMusicDefaultSeeded = "music_default_profile_seeded"

// DefaultMusicPreset is the preset the seeded profile is made from.
const DefaultMusicPreset = "Lossless, or the best MP3"

// EnsureDefaultMusicProfile creates the default music profile when no music profile exists
// and it has never been seeded, then marks the seeding done. created reports whether it
// made one. Safe to call on every start.
func (s *Service) EnsureDefaultMusicProfile(ctx context.Context) (created bool, err error) {
	if s.settings.GetBool(ctx, KeyMusicDefaultSeeded, false) {
		return false, nil
	}
	existing, err := s.repo.List(ctx, MediaMusic)
	if err != nil {
		return false, err
	}
	if len(existing) == 0 {
		var preset *MusicPreset
		for _, p := range MusicPresets() {
			if p.Name == DefaultMusicPreset {
				preset = &p
				break
			}
		}
		if preset == nil {
			return false, fmt.Errorf("no %q music preset", DefaultMusicPreset)
		}
		scores := make(map[string]int, len(preset.FormatScores))
		for k, v := range preset.FormatScores {
			scores[k] = v
		}
		// Upgrades off: a profile made without anyone looking mustn't start replacing the
		// albums a library already has. The owner turns them on in the profile.
		if _, err := s.Create(ctx, StoredProfile{
			MediaType: MediaMusic, Name: preset.Name, FormatScores: scores,
			MinFormatScore: preset.MinFormatScore, UpgradesEnabled: false,
		}); err != nil {
			return false, err
		}
		created = true
	}
	// Marked even when a profile already existed: deleting every music profile later must
	// not bring this one back.
	return created, s.settings.SetBool(ctx, KeyMusicDefaultSeeded, true)
}
