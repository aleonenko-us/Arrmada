package music

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// AudioFile is one audio file found in a download, ready to be matched to a track.
type AudioFile struct {
	Path      string
	SizeBytes int64
	// Disc is a hint from where the file sits (a "CD2" folder, say): the disc it belongs
	// to, or 0 when nothing says. It is only used when the filename carries a track number
	// alone.
	Disc int
}

var (
	// reDiscTrack is the compact disc-track prefix: "1-04 Airbag", "2.07 X", "1-04.flac".
	// No spaces inside: "04 - 99 Problems" is track 4 of a song called "99 Problems", not
	// disc 4 track 99. Even the compact shape is only believed per folder (MatchTracks).
	reDiscTrack = regexp.MustCompile(`^\s*(\d{1,2})[-.](\d{2,3})(?:[\s._)-]|$)`)
	// reTrack is a plain leading track number: "01 - Airbag", "01. Airbag", "01_Airbag".
	reTrack = regexp.MustCompile(`^\s*(\d{1,3})\s*[-._)\s]`)
)

// numbering is what a filename's leading number says.
type numbering struct {
	disc, track int
	compact     bool   // read off a "1-04" disc-track prefix
	used        string // the prefix it was read from, which the title then loses
}

// readNumber reads a file's leading number. compactOK says whether the disc-track shape
// may be believed for this file (see MatchTracks); without it only the track number is
// read and the disc is left unknown (0).
func readNumber(base string, compactOK bool) numbering {
	if compactOK {
		if m := reDiscTrack.FindStringSubmatch(base); m != nil {
			d, _ := strconv.Atoi(m[1])
			n, _ := strconv.Atoi(m[2])
			if d > 0 && n > 0 && n <= 200 {
				return numbering{disc: d, track: n, compact: true, used: m[0]}
			}
		}
	}
	m := reTrack.FindStringSubmatch(base)
	if m == nil {
		return numbering{}
	}
	n, _ := strconv.Atoi(m[1])
	if n <= 0 {
		return numbering{}
	}
	return numbering{track: n, used: m[0]}
}

// baseName is a file's name without its directory or extension.
func baseName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// TrackNumberOf reads the disc and track number off a single filename. Returns (0, 0) when
// the name carries no number — the caller must then fall back to matching by title,
// because guessing a position from directory order silently mislabels an album.
//
// On its own a file can't say whether "1-04" is a disc-track pair, so this believes the
// compact shape; MatchTracks, which sees the whole folder, is stricter.
func TrackNumberOf(path string) (disc, track int) {
	num := readNumber(baseName(path), true)
	// A "number" above 200 is a year or a bitrate that leaked into the name, not a track.
	if num.track <= 0 || num.track > 200 {
		return 0, 0
	}
	if num.disc == 0 {
		num.disc = 1
	}
	return num.disc, num.track
}

// titleOf strips the number prefix actually used and the extension, leaving the song
// title. Only that prefix goes: "04 - 99 Problems" keeps its "99".
func titleOf(path, used string) string {
	return strings.Trim(strings.TrimPrefix(baseName(path), used), " -_.")
}

// MatchTracks maps a download's audio files onto an album's tracks.
//
// Numbering first, title second, and nothing else. Position-in-directory is deliberately NOT
// used as a fallback: a folder that happens to hold a bonus track, an intro file or a
// different edition would shift every song after it, and an album silently labelled one
// track out is far worse than one left unmatched for a human to look at.
//
// The number is read carefully, because song titles start with numbers too ("99 Problems",
// "7 Rings"):
//   - A compact disc-track prefix ("1-04") is believed only when at least two files in the
//     same folder are shaped that way and the disc exists on the listing. Otherwise every
//     file in the folder is read as a plain track number.
//   - A three-digit number on a multi-disc listing ("101 - X") is disc 1, track 1.
//   - When a file's title names exactly one track, and it's a different track from the one
//     its number points at, the title wins. When the title names nothing, the number
//     stands. This catches the one-off misfile a stray leading number would cause.
//
// Files that match nothing are returned so the caller can report them rather than drop them
// in silence.
func MatchTracks(tracks []Track, files []AudioFile) (matched map[int64]AudioFile, unmatched []AudioFile) {
	matched = make(map[int64]AudioFile, len(tracks))
	claimed := make(map[string]bool, len(files))

	maxDisc := 1
	byNumber := make(map[[2]int]Track, len(tracks))
	byTitle := make(map[string][]Track, len(tracks))
	for _, t := range tracks {
		byNumber[[2]int{t.DiscNumber, t.TrackNumber}] = t
		if t.DiscNumber > maxDisc {
			maxDisc = t.DiscNumber
		}
		if k := NormKey(t.Title); k != "" {
			byTitle[k] = append(byTitle[k], t)
		}
	}
	titleMatch := func(s string) (Track, bool) {
		if cands := byTitle[NormKey(s)]; len(cands) == 1 {
			return cands[0], true
		}
		return Track{}, false // no match, or ambiguous — leave it to the number or a human
	}

	// The disc-track shape is decided per folder: one lone "04-99 Problems" is a title.
	compactIn := map[string]int{}
	for _, f := range files {
		if reDiscTrack.MatchString(baseName(f.Path)) {
			compactIn[filepath.Dir(f.Path)]++
		}
	}

	for _, f := range files {
		base := baseName(f.Path)
		num := readNumber(base, compactIn[filepath.Dir(f.Path)] >= 2)
		if num.compact && num.disc > maxDisc {
			// A disc the listing doesn't have: the prefix wasn't a disc after all.
			num = readNumber(base, false)
		}
		if !num.compact && num.track >= 100 && maxDisc > 1 {
			num.disc, num.track = num.track/100, num.track%100 // "101 - X" on a 2-CD album
		}
		if num.track > 200 {
			num = numbering{} // a year or a bitrate, not a track
		}
		if !num.compact && num.disc == 0 && f.Disc > 0 {
			num.disc = f.Disc
		}

		var byNum Track
		haveNum := false
		if num.track > 0 {
			if num.disc > 0 {
				byNum, haveNum = byNumber[[2]int{num.disc, num.track}]
			} else if byNum, haveNum = byNumber[[2]int{1, num.track}]; !haveNum {
				// No disc said and not on disc 1: the one track with that number anywhere.
				// A lone "07" on a two-disc album is ambiguous, so exactly one candidate.
				if cands := tracksWithNumber(tracks, num.track); len(cands) == 1 {
					byNum, haveNum = cands[0], true
				}
			}
		}

		// The whole name first ("7 Rings" with no prefix at all, which would otherwise
		// read as track 7), then the name with the number prefix taken off.
		byName, haveName := titleMatch(base)
		if !haveName && num.used != "" {
			byName, haveName = titleMatch(titleOf(f.Path, num.used))
		}

		var t Track
		switch {
		case haveName:
			t = byName // agrees with the number, or the number pointed at the wrong song
		case haveNum:
			t = byNum
		default:
			continue
		}
		if _, taken := matched[t.ID]; taken {
			continue
		}
		matched[t.ID] = f
		claimed[f.Path] = true
	}

	for _, f := range files {
		if !claimed[f.Path] {
			unmatched = append(unmatched, f)
		}
	}
	return matched, unmatched
}

func tracksWithNumber(tracks []Track, n int) []Track {
	var out []Track
	for _, t := range tracks {
		if t.TrackNumber == n {
			out = append(out, t)
		}
	}
	return out
}

// ReleaseIsForAlbum reports whether a release name names this artist and album as whole
// words — the gate that stops a search for one album grabbing a different one by the same
// artist, which is exactly the hole the Books module had. Names are compared folded (see
// foldMusic), so "Bjork - Homogenic" is Björk's and "ACDC - Back in Black" is AC/DC's.
//
// A discography release is refused outright: grabbing one against a single album would fill
// that album's folder with the artist's entire catalogue.
func ReleaseIsForAlbum(releaseName, artist, album string) bool {
	if ParseRelease(releaseName).Discography {
		return false
	}
	return artistNamed(releaseName, artist) && containsWords(releaseName, album)
}

// ReleaseIsDiscographyFor reports whether a release is a whole-catalogue pack for this
// artist. Kept separate from ReleaseIsForAlbum, which refuses discographies outright:
// they're only ever wanted through the explicit "grab discography" action, where a
// multi-album importer maps each folder to its own album.
func ReleaseIsDiscographyFor(releaseName, artist string) bool {
	return ParseRelease(releaseName).Discography && artistNamed(releaseName, artist)
}
