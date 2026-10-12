package quality

// duneRuntimeMin is Dune: Part Two's runtime — attached to the sample candidates so the preview
// reflects a profile's bitrate ceiling (82 GB ≈ 71 Mbps, 24 GB ≈ 21 Mbps at this length).
const duneRuntimeMin = 166

// SampleCandidates is a fixed set of real-world releases (Dune: Part Two) used
// by the quality-preview endpoint and docs to demonstrate the engine without a
// configured indexer. Mirrors the approved mockup.
func SampleCandidates() []Candidate {
	cands := []Candidate{
		NewCandidate("Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.TrueHD.Atmos.7.1-FraMeSToR", 82, 44),
		NewCandidate("Dune.Part.Two.2024.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR.H.265-FLUX", 24, 212),
		NewCandidate("Dune.Part.Two.2024.2160p.WEB-DL.DDP5.1.HDR.H.265-RUMOUR", 19, 61),
		NewCandidate("Dune.Part.Two.2024.1080p.BluRay.x264.DTS-HD.MA.5.1-SPARKS", 16, 180),
		NewCandidate("Dune.Part.Two.2024.1080p.WEB-DL.DDP5.1.H.264-EVO", 9, 340),
		NewCandidate("Dune.Part.Two.2024.1080p.WEBRip.x265.AAC5.1-YTS", 3.8, 990),
		NewCandidate("Dune.Part.Two.2024.720p.HDTV.x264-GALAXY", 1.5, 22),
	}
	for i := range cands {
		cands[i].RuntimeMin = duneRuntimeMin
	}
	return cands
}

// seriesEpisodeMin is the sample show's episode length; a pack carries the sum of its episodes.
const seriesEpisodeMin = 50

// SeriesSampleCandidates is the series preview's sample set: what a search for one episode of
// a 50-minute show turns up — episode-sized files, scene WEB, a fansub, a season pack and a dead
// torrent — so a series profile's ceiling and floor are judged at episode length rather than a
// film's. The show and every group are made up, so none of it reads as real indexer data.
//
// At 50 minutes 1 GB is about 2.9 Mb/s: the 8 GB BluRay is ~23 Mb/s (over any TV ceiling),
// the HEVC WEB-DL ~5.2 and the scene WEB ~5.7; the pack covers ten episodes at ~7.2 Mb/s.
func SeriesSampleCandidates() []Candidate {
	ep := func(name string, gb float64, seeders int) Candidate {
		return NewCandidate(name, gb, seeders).WithRuntime(seriesEpisodeMin)
	}
	return []Candidate{
		ep("Harbor.Lights.S02E05.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR.H.265-NIMBUS", 6, 48),
		ep("Harbor.Lights.S02E05.1080p.WEB.h264-KESTREL", 2, 310),
		ep("Harbor.Lights.S02E05.1080p.WEB-DL.DDP5.1.x265-QUILLFEATHER", 1.8, 125),
		ep("Harbor.Lights.S02E05.1080p.BluRay.DTS-HD.MA.5.1.x265-BRASSWORK", 8, 37),
		ep("Harbor.Lights.S02E05.720p.HDTV.x264-LANTERN", 0.9, 64),
		ep("[Tidepool] Harbor Lights - 17 (1080p) [ABCD1234]", 1.4, 90),
		NewCandidate("Harbor.Lights.S02.1080p.WEB-DL.DDP5.1.H.264-KESTREL", 25, 140).WithRuntime(10 * seriesEpisodeMin),
		ep("Harbor.Lights.S02E05.1080p.WEB-DL.DDP5.1.x265-DRIFTWOOD", 1.9, 0),
	}
}

// Samples is the preview's sample set for a media type, with the caption that says what it
// is. Every type that isn't series gets the film.
func Samples(media string) ([]Candidate, string) {
	if media == MediaSeries {
		return SeriesSampleCandidates(), "An episode of a 50-minute show"
	}
	return SampleCandidates(), "Dune: Part Two (2024), 166 min"
}
