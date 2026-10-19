package music

import (
	"fmt"
	"sort"
	"strings"
)

// Editions (MUS-30). Every release of an album is stored; one is its target (what
// completeness and imports are measured against), chosen by a stated policy unless a
// person pinned one. Editions with the same tracks are interchangeable: a correct download
// of any of them satisfies the album, and the matcher never asks a person to pick between
// them.

// Edition policies (setting music_edition_policy).
const (
	// PolicyOriginal: the earliest acceptable release of the group; ties go to Digital
	// Media, then CD, then the fewest tracks. The owner's default.
	PolicyOriginal = "original"
	// PolicyMostComplete: the most tracks; ties go to the earliest.
	PolicyMostComplete = "most_complete"
	// PolicyPreferred: ordered country and media lists ("GB, US, XW"; "Digital Media,
	// CD"); ties go to the earliest.
	PolicyPreferred = "preferred"
)

// EditionPolicy is how a target edition is picked.
type EditionPolicy struct {
	Mode      string   `json:"mode"`
	Countries []string `json:"countries,omitempty"` // preferred mode, in order
	Media     []string `json:"media,omitempty"`     // preferred mode, in order
}

// ValidPolicy reports whether mode is a policy.
func ValidPolicy(mode string) bool {
	switch mode {
	case PolicyOriginal, PolicyMostComplete, PolicyPreferred:
		return true
	}
	return false
}

// ignoredMedia are disc formats that never hold an album's audio as files people
// download: video discs. An edition made only of them isn't acceptable, and a video disc
// in an otherwise audio edition (a deluxe's bonus DVD) is left out of matching and
// completeness.
var ignoredMedia = map[string]bool{
	"dvd": true, "dvdvideo": true, "bluray": true, "vhs": true, "hddvd": true,
	"dualdiscdvdside": true, "videocd": true, "vcd": true, "svcd": true, "laserdisc": true, "umd": true,
}

// IgnoredMedium reports whether a disc format is one matching ignores.
func IgnoredMedium(format string) bool {
	k := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return -1
	}, format)
	return ignoredMedia[k]
}

// editionFormats are an edition's disc formats: its discs' when stored, else read off the
// media summary ("2×CD + DVD-Video").
func editionFormats(e Edition) []string {
	var out []string
	for _, m := range e.Media {
		out = append(out, m.Format)
	}
	if len(out) > 0 {
		return out
	}
	for _, part := range strings.Split(e.MediaSummary, "+") {
		part = strings.TrimSpace(part)
		if i := strings.Index(part, "×"); i >= 0 {
			part = strings.TrimSpace(part[i+len("×"):])
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// firstSummaryFormat is the first disc format a media summary names.
func firstSummaryFormat(summary string) string {
	for _, f := range editionFormats(Edition{MediaSummary: summary}) {
		if !IgnoredMedium(f) {
			return f
		}
	}
	return ""
}

// EditionAcceptable reports whether an edition may be an album's target or satisfy it:
// an official release (or one whose status MusicBrainz doesn't give) with at least one
// audio disc. A promotion, bootleg or pseudo-release only counts when a person pinned it.
func EditionAcceptable(e Edition, pinned bool) bool {
	switch strings.ToLower(strings.TrimSpace(e.Status)) {
	case "", "official":
	default:
		if !pinned {
			return false
		}
	}
	formats := editionFormats(e)
	if len(formats) == 0 {
		return true // nothing says it's video
	}
	for _, f := range formats {
		if !IgnoredMedium(f) {
			return true
		}
	}
	return false
}

// equivalentLengthMs is how far two tracks' lengths may differ for their editions to
// still be the same.
const equivalentLengthMs = 3000

// EditionsEquivalent reports whether two editions are the same tracks: as many, with
// fold-equal titles in order and lengths within 3 s (where both know them). It needs both
// track lists; an edition known only by its summary is equivalent to nothing.
func EditionsEquivalent(a, b Edition) bool {
	ta, tb := audioTracks(a), audioTracks(b)
	if len(ta) == 0 || len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		if NormKey(ta[i].Title) != NormKey(tb[i].Title) {
			return false
		}
		if ta[i].LengthMs > 0 && tb[i].LengthMs > 0 && absInt(ta[i].LengthMs-tb[i].LengthMs) > equivalentLengthMs {
			return false
		}
	}
	return true
}

// audioTracks are an edition's tracks in order, video discs left out.
func audioTracks(e Edition) []ReleaseTrack {
	var out []ReleaseTrack
	for _, m := range e.Media {
		if IgnoredMedium(m.Format) {
			continue
		}
		out = append(out, m.Tracks...)
	}
	return out
}

// mediaRank orders formats for the original policy's tie-break: Digital Media, then CD,
// then everything else.
func mediaRank(e Edition) int {
	f := strings.ToLower(primaryFormat(e))
	switch {
	case f == "digital media":
		return 0
	case f == "cd" || strings.HasSuffix(f, "cd"): // CD, Enhanced CD, HDCD, SHM-CD…
		return 1
	}
	return 2
}

// dateBefore compares two MusicBrainz dates of possibly different precision: -1 when a
// is earlier, 1 when later, 0 when they can't be told apart ("1997" and "1997-05-21"). An
// undated release sorts after every dated one.
func dateBefore(a, b string) int {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	switch {
	case a[:n] < b[:n]:
		return -1
	case a[:n] > b[:n]:
		return 1
	}
	return 0
}

// PickEdition chooses an album's target edition by policy among the acceptable ones
// (pinned editions are the person's choice and never go through here). ok is false when
// none is acceptable.
func PickEdition(eds []Edition, p EditionPolicy) (Edition, bool) {
	var cands []Edition
	for _, e := range eds {
		if !e.Legacy && EditionAcceptable(e, false) {
			cands = append(cands, e)
		}
	}
	if len(cands) == 0 {
		return Edition{}, false
	}
	earliest := func(i, j int) int { return dateBefore(cands[i].ReleaseDate, cands[j].ReleaseDate) }
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		switch p.Mode {
		case PolicyMostComplete:
			if a.TrackCount != b.TrackCount {
				return a.TrackCount > b.TrackCount
			}
			if c := earliest(i, j); c != 0 {
				return c < 0
			}
		case PolicyPreferred:
			if pa, pb := priority(a.Country, p.Countries), priority(b.Country, p.Countries); pa != pb {
				return pa < pb
			}
			if ma, mb := priority(primaryFormat(a), p.Media), priority(primaryFormat(b), p.Media); ma != mb {
				return ma < mb
			}
			if c := earliest(i, j); c != 0 {
				return c < 0
			}
		default: // original
			if c := earliest(i, j); c != 0 {
				return c < 0
			}
		}
		if ra, rb := mediaRank(a), mediaRank(b); ra != rb {
			return ra < rb
		}
		if a.TrackCount != b.TrackCount {
			return a.TrackCount < b.TrackCount
		}
		return a.ID < b.ID
	})
	return cands[0], true
}

// EditionLabel describes an edition the way Reviews and the picker say it:
// "GB CD 1997 · 12 tracks · deluxe edition".
func EditionLabel(e Edition) string {
	var parts []string
	head := strings.TrimSpace(strings.Join(nonEmptyStrings(e.Country, mediaWords(e), yearWord(e.ReleaseDate)), " "))
	if head != "" {
		parts = append(parts, head)
	}
	if n := e.TrackCount; n > 0 {
		parts = append(parts, fmt.Sprintf("%d tracks", n))
	} else if n := len(audioTracks(e)); n > 0 {
		parts = append(parts, fmt.Sprintf("%d tracks", n))
	}
	if e.Disambiguation != "" {
		parts = append(parts, e.Disambiguation)
	}
	if e.Legacy {
		parts = append(parts, "from before the upgrade")
	}
	if len(parts) == 0 {
		return "edition " + fmt.Sprint(e.ID)
	}
	return strings.Join(parts, " · ")
}

func mediaWords(e Edition) string {
	if e.MediaSummary != "" {
		return e.MediaSummary
	}
	return primaryFormat(e)
}

func yearWord(date string) string {
	if y := yearOf(date); y > 0 {
		return fmt.Sprint(y)
	}
	return ""
}

func nonEmptyStrings(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// trackIdent is what a release track is, for moving file rows between editions.
type trackIdent struct {
	ID, RecordingID     int64
	MBID, RecordingMBID string
	Disc, Position      int
	Title               string
}

// mapTracks maps tracks of one edition onto another's, strongest evidence first, each
// target used once:
//  1. the same recording (by row, or by MBID);
//  2. the same release-track MBID (a legacy edition's tracks carry the release-track ids
//     of the release they were read from);
//  3. the same (disc, position) with a title that is nearly the same;
//  4. a title only one track of the other edition has.
//
// A track that fits none is left out of the map. Deliberately stricter than "the same
// position": a remaster that reorders or renames tracks must not move a file onto the
// wrong song.
func mapTracks(from, to []trackIdent) map[int64]int64 {
	out := map[int64]int64{}
	used := map[int64]bool{}
	pass := func(match func(f, t trackIdent) bool) {
		for _, f := range from {
			if _, done := out[f.ID]; done {
				continue
			}
			for _, t := range to {
				if !used[t.ID] && match(f, t) {
					out[f.ID], used[t.ID] = t.ID, true
					break
				}
			}
		}
	}
	pass(func(f, t trackIdent) bool {
		return (f.RecordingID != 0 && f.RecordingID == t.RecordingID) ||
			(f.RecordingMBID != "" && strings.EqualFold(f.RecordingMBID, t.RecordingMBID))
	})
	pass(func(f, t trackIdent) bool { return f.MBID != "" && strings.EqualFold(f.MBID, t.MBID) })
	pass(func(f, t trackIdent) bool {
		return f.Disc == t.Disc && f.Position == t.Position && stringDist(f.Title, t.Title) <= 0.2
	})
	titles := map[string]int{}
	for _, t := range to {
		titles[NormKey(t.Title)]++
	}
	pass(func(f, t trackIdent) bool {
		k := NormKey(f.Title)
		return k != "" && k == NormKey(t.Title) && titles[k] == 1
	})
	return out
}
