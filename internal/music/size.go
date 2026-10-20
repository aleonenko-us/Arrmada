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
// in interactive search. Everything else errs towards letting a release through: a
// refused good release is a missed album, and the matcher still checks what arrives.

// Why a release fails the sanity check (the codes a search records; never rename one).
const (
	SaneVideo = "video" // listed in a video category, or named like a video release
	SaneSize  = "size"  // far too small or too big for the album in its tier
)

// The markers in a release name, each read as a whole word.
var (
	// reVideoRes is a picture resolution: on its own, proof of a video.
	reVideoRes = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:2160p|1080[pi]|720p|576p|480p|4k\s?uhd|uhd\s?blu-?ray)(?:$|[^a-z0-9])`)
	// reVideoDisc is a video disc's own structure or container, or a source only video
	// comes from (WEB-DL, WEBRip, DVDRip, HDTV, BRRip, BDRemux): also proof. Plain "WEB" is
	// how music sold online is tagged, so it isn't one.
	reVideoDisc = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:dvd[59]|video_ts|bdmv|m2ts|mkv|web-?dl|webrip|dvdrip|hdtv|brrip|bdremux)(?:$|[^a-z0-9])`)
	// reVideoCodec is a video codec, and reVideoRip a source Blu-ray audio shares with
	// video ("BDRip FLAC", a "Remux", "Blu-ray"). Either alone isn't proof (a band can be
	// called HEVC), so it takes both together.
	reVideoCodec = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:x\.?26[45]|h\.?26[45]|hevc|avc1?|xvid|divx|vc-?1)(?:$|[^a-z0-9])`)
	reVideoRip   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:bdrip|remux|blu-?ray)(?:$|[^a-z0-9])`)
	// reHiRes24 marks hi-res audio: 24-bit, or a high sample rate up to DXD.
	reHiRes24 = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:24\s?-?bits?|24b|24[-/ ](?:44|48|88|96|176|192|352|384)(?:\.\d)?|dxd|352\.?8|384\s?khz|192\s?khz|176\.4|hi-?res)(?:$|[^a-z0-9])`)
	// reBigAudio marks an audio release far bigger than its stereo tier: Blu-ray, DVD-Audio
	// and SACD rips (hi-res, often 5.1 beside the stereo), and multichannel mixes.
	reBigAudio = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:bd-?a|bd-?audio|blu-?ray(?:\s?audio)?|bd|bdrip|dvd-?a|dvd-?audio|sacd|5\.1|7\.1|mch|multi-?channel|surround|atmos)(?:$|[^a-z0-9])`)
	// reExtras marks scans or artwork packed beside the audio.
	reExtras = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:scans?|artwork|booklet|covers)(?:$|[^a-z0-9])`)
)

// LooksLikeVideo reports whether a release name shows it is video: a resolution, a video
// disc's structure, or a video codec together with a video source.
func LooksLikeVideo(name string) bool {
	return reVideoRes.MatchString(name) || reVideoDisc.MatchString(name) ||
		(reVideoCodec.MatchString(name) && reVideoRip.MatchString(name))
}

// newznabCategory reports whether a category number is one of Newznab's standard ones
// (1000-8999). Trackers also send their own numbers (TorrentLeech's, MyAnonaMouse's, a
// Jackett tracker's 100000+), which say nothing comparable.
func newznabCategory(cat int) bool { return cat >= 1000 && cat < 9000 }

// musicCategory reports whether a Newznab category is audio a music album can be listed
// under: Audio (3000) and its subcategories, except Audio/Video (3020, music videos and
// concerts) and Audiobook (3030).
func musicCategory(cat int) bool {
	return cat >= 3000 && cat < 4000 && cat != 3020 && cat != 3030
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

// tierFloorMBPerMin is the smallest a tier's minute plausibly gets, where that's well under
// its usual size: Opus and Ogg are good at 64-96 kbps.
var tierFloorMBPerMin = map[Quality]float64{QualityOpus: 0.45, QualityOGG: 0.6}

// The size window, as fractions of the expected size: under 35% is too little audio for
// the album in that tier (a fake, a sample, one disc of two), over 400% is something else
// (a video, a multi-album pack, the album in every format at once).
const (
	sizeFloor   = 0.35
	sizeCeiling = 4.0
	// losslessFloor is lossless's floor: a mono or quiet 16-bit FLAC compresses to a fifth
	// of a typical one.
	losslessFloor = 0.2
	// bigAudioCeiling is the ceiling for a release named as Blu-ray, DVD-Audio, SACD or
	// multichannel audio: several mixes of hi-res audio in one release.
	bigAudioCeiling = 12.0
	// extrasMB is what scans or artwork packed with the audio may add.
	extrasMB = 500
	// manyDiscs is the target edition's disc count from which a release isn't held to the
	// whole listing's size: a box set's one-disc release is still that album's music.
	manyDiscs = 3
)

// SizeListing is what the size check knows about the album: how long its listing runs and
// over how many discs. Seconds is 0 when it isn't known well enough to judge a size by.
type SizeListing struct {
	Seconds int
	Discs   int
}

// fallbackTrackSec is a typical track's length, for telling a listing whose lengths look
// wrong.
const fallbackTrackSec = 240

// ListingOf reads a listing for the size check. Any track without a length, or a total
// under 60% of four minutes a track (lengths that can't be right), leaves Seconds at 0 and
// the size unjudged.
func ListingOf(tracks []Track) SizeListing {
	l := SizeListing{}
	discs := map[int]bool{}
	total := 0
	for _, t := range tracks {
		discs[t.DiscNumber] = true
		if t.DurationSec <= 0 {
			return SizeListing{Discs: len(discs)}
		}
		total += t.DurationSec
	}
	l.Discs = len(discs)
	if len(tracks) == 0 || float64(total) < 0.6*float64(len(tracks)*fallbackTrackSec) {
		return l
	}
	l.Seconds = total
	return l
}

// ReleaseSane checks a release found for an album: its categories, its name and its size
// against the album's listing in the tier its name claims. ok is false with a code
// (SaneVideo, SaneSize) and a sentence saying why. A check it has nothing to go on for (no
// Newznab categories, an unknown size, tier or listing) passes: the filter only refuses
// what it can show is wrong.
func ReleaseSane(name string, categories []int, sizeBytes int64, tier Quality, listing SizeListing) (ok bool, code, reason string) {
	// Only Newznab's own categories are judged; a tracker's native numbers are skipped.
	standard, music, video := 0, false, false
	for _, c := range categories {
		if !newznabCategory(c) {
			continue
		}
		standard++
		music = music || musicCategory(c)
		video = video || videoCategory(c)
	}
	if standard > 0 && !music {
		if video {
			return false, SaneVideo, "listed as video"
		}
		return false, SaneVideo, "listed outside the music categories"
	}
	if LooksLikeVideo(name) {
		return false, SaneVideo, "looks like a video release"
	}
	rate, known := tierMBPerMin[tier]
	if !known || sizeBytes <= 0 || listing.Seconds <= 0 {
		return true, "", ""
	}
	minutes := float64(listing.Seconds) / 60
	floorRate := rate
	if f, ok := tierFloorMBPerMin[tier]; ok {
		floorRate = f
	}
	// A lossless name often doesn't say it's hi-res ("FLAC 24-192" reads as FLAC), so any
	// lossless tier may be as big as hi-res is.
	high := minutes * rate * sizeCeiling
	if IsLosslessQuality(tier) {
		high = minutes * tierMBPerMin[QualityFLAC24] * sizeCeiling
	}
	if reBigAudio.MatchString(name) || (IsLosslessQuality(tier) && reHiRes24.MatchString(name)) {
		// Disc rips, multichannel, and hi-res up to DXD (352.8 kHz, ~70 MB a minute).
		high = minutes * tierMBPerMin[QualityFLAC24] * bigAudioCeiling
	}
	floor := sizeFloor
	if IsLosslessQuality(tier) {
		floor = losslessFloor
	}
	if reExtras.MatchString(name) {
		high += extrasMB
	}
	got := float64(sizeBytes) / (1 << 20)
	switch {
	case listing.Discs < manyDiscs && got < minutes*floorRate*floor:
		return false, SaneSize, fmt.Sprintf("%s is too small for %d minutes of %s", sizeWords(sizeBytes), int(minutes+0.5), tier)
	case got > high:
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
