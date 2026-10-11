package quality

import "context"

// PrefersDolbyVision reports whether the profile a title runs under (ref, resolved through
// Effective for media) wants Dolby Vision: its target file prefers or requires it, it
// requires the Dolby Vision format, or scores it up. Convert asks before dropping a file's
// Dolby Vision layer, which would leave the file outside that profile's target.
func (s *Service) PrefersDolbyVision(ctx context.Context, ref, media string) bool {
	sp, err := s.GetStored(ctx, s.Effective(ctx, ref, media))
	if err != nil {
		return false
	}
	if sp.Ideal != nil {
		switch sp.Ideal.HDR["DV"] {
		case PrefWant, PrefMust:
			return true
		}
	}
	for _, f := range sp.RequiredFormats {
		if f == "Dolby Vision" {
			return true
		}
	}
	return sp.FormatScores["Dolby Vision"] > 0
}
