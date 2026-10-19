package music

import (
	"fmt"
	"regexp"
)

// The sanity check a release must pass before it can be grabbed for an album (MUS-21): it
// has to be audio, and about the size the album is in the tier its name claims.
//
// Most indexers are asked for audio categories only, but a mixed-category tracker answers
// a text search with whatever it has, and its concert films carry "FLAC" in their names
// ("1080p BluRay FLAC 2.0 x264") and score like a lossless album. Fakes do the opposite: a
// 30 MB "FLAC" can win on seeders. Both are refused here, with a reason a person can read
// in interactive search.

// Why a release fails the sanity check (the codes a search records; never rename one).
const (
	SaneVideo = "video" // listed in a video category, or named like a video release
	SaneSize  = "size"  // far too small or too big for the album in its tier
)

// reVideoRelease is the markers only a video release carries: a picture resolution, a video
// codec, a video source or rip. Read as whole words. Bare "BluRay" isn't one: Blu-ray
// audio discs are ripped to FLAC and named for the disc, and a concert film nearly always
// names its resolution or codec as well.
var reVideoRelease = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:2160p|1080[pi]|720p|576p|480p|4k\s?uhd|uhd\s?bluray|x\.?26[45]|h\.?26[45]|hevc|avc1|xvid|divx|web-?dl|webrip|bdrip|brrip|bdremux|remux|dvdrip|dvd-?r|dvd[59]|video_ts|mkv|m2ts)(?:$|[^a-z0-9])`)

// LooksLikeVideo reports whether a release name carries a video marker.
func LooksLikeVideo(name string) bool { return reVideoRelease.MatchString(name) }

// musicCategory reports whether a Newznab category is audio a music album can be listed
// under: Audio (3000) and its subcategories, except Audio/Video (3020, music videos and
// concerts) and Audiobook (3030). A tracker's own category (100000 and up) says nothing
// either way, so it passes.
func musicCategory(cat int) bool {
	switch {
	case cat >= 100000:
		return true
	case cat == 3020, cat == 3030:
		return false
	}
	return cat >= 3000 && cat < 4000
}

// videoCategory reports whether a Newznab category is video: Movies (2000s), TV (5000s),
// or Audio/Video (3020).
func videoCategory(cat int) bool {
	return cat == 3020 || (cat >= 2000 && cat < 3000) || (cat >= 5000 && cat < 6000)
}

// tierMBPerMin is roughly how many megabytes a minute of audio takes in each tier: the
// middle of what real releases measure, so the window around it can be generous.
var tierMBPerMin = map[Quality]float64{
	QualityFLAC24: 20, QualityFLAC: 8, QualityALAC: 8, QualityWAV: 10,
	QualityMP3320: 2.4, QualityMP3V0: 1.8, QualityAAC256: 1.9, QualityMP3256: 1.9,
	QualityMP3V2: 1.4, QualityMP3192: 1.4, QualityOpus: 1.2, QualityOGG: 1.2, QualityMP3128: 1,
}

// The size window, as fractions of the expected size: under 35% is too little audio for
// the album in that tier (a fake, a sample, one disc of two), over 400% is something else
// (a video, a multi-album pack, the album in every format at once).
const (
	sizeFloor   = 0.35
	sizeCeiling = 4.0
)

// fallbackTrackSec stands in for a track whose length MusicBrainz doesn't list.
const fallbackTrackSec = 240

// ListingSeconds is how long an album's listing runs: its tracks' lengths, four minutes for
// any track without one. 0 when there's no listing.
func ListingSeconds(tracks []Track) int {
	total := 0
	for _, t := range tracks {
		if t.DurationSec > 0 {
			total += t.DurationSec
		} else {
			total += fallbackTrackSec
		}
	}
	return total
}

// ReleaseSane checks a release found for an album: its categories, its name and its size
// against the album's length (listingSec, from ListingSeconds) in the tier its name claims.
// ok is false with a code (SaneVideo, SaneSize) and a sentence saying why. A check it has
// nothing to go on for (no categories, an unknown size, tier or listing) passes: the
// filter only refuses what it can show is wrong.
func ReleaseSane(name string, categories []int, sizeBytes int64, tier Quality, listingSec int) (ok bool, code, reason string) {
	if len(categories) > 0 {
		music, video := false, false
		for _, c := range categories {
			music = music || musicCategory(c)
			video = video || videoCategory(c)
		}
		if !music {
			if video {
				return false, SaneVideo, "listed as video"
			}
			return false, SaneVideo, fmt.Sprintf("listed outside the music categories (%d)", categories[0])
		}
	}
	if LooksLikeVideo(name) {
		return false, SaneVideo, "looks like a video release"
	}
	rate, known := tierMBPerMin[tier]
	if !known || sizeBytes <= 0 || listingSec <= 0 {
		return true, "", ""
	}
	minutes := float64(listingSec) / 60
	expected := minutes * rate
	// A lossless name often doesn't say it's hi-res ("FLAC 24-192" reads as FLAC), so any
	// lossless tier may be as big as hi-res is.
	high := expected
	if IsLosslessQuality(tier) {
		high = minutes * tierMBPerMin[QualityFLAC24]
	}
	got := float64(sizeBytes) / (1 << 20)
	switch {
	case got < expected*sizeFloor:
		return false, SaneSize, fmt.Sprintf("%s is too small for %d minutes of %s", sizeWords(sizeBytes), int(minutes+0.5), tier)
	case got > high*sizeCeiling:
		return false, SaneSize, fmt.Sprintf("%s is too big for %d minutes of %s", sizeWords(sizeBytes), int(minutes+0.5), tier)
	}
	return true, "", ""
}

// sizeWords is a release size as a person reads it: "30 MB", "1.4 GB".
func sizeWords(b int64) string {
	mb := float64(b) / (1 << 20)
	if mb < 1024 {
		return fmt.Sprintf("%.0f MB", mb)
	}
	return fmt.Sprintf("%.1f GB", mb/1024)
}
