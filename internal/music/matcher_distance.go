package music

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// The matcher's distance (MUS-31), beets' arithmetic: every component adds penalty ×
// weight, and the distance is that sum over the largest it could have been, from 0.0 (a
// perfect fit) to 1.0. A component is only added when both sides know the thing it
// compares, so a missing tag neither helps nor hurts.
//
// The weights are beets' defaults. Lidarr swapped missing_tracks and unmatched_tracks
// (0.6 / 0.9) and doubled the recording id to 10.0; Arrmada keeps beets' values: a track
// with no file usually means the wrong edition, which should cost more than an extra file
// (a hidden track, an interlude), and a stale recording id shouldn't outweigh everything
// else. Barcode and ISRC are Arrmada's additions, compared only when both sides have one.
// Code constants with one table test (TestMatchWeights), not settings.
const (
	wAlbumID       = 5.0
	wArtist        = 3.0
	wAlbum         = 3.0
	wTracks        = 2.0
	wMissingTracks = 0.9
	wUnmatched     = 0.6
	wDataSource    = 2.0
	wMediaCount    = 1.0
	wMediaFormat   = 1.0
	wYear          = 1.0
	wCountry       = 0.5
	wLabel         = 0.5
	wCatalogNumber = 0.5
	wAlbumDisambig = 0.5
	wBarcode       = 1.0

	wTrackTitle  = 3.0
	wTrackArtist = 2.0
	wTrackIndex  = 1.0
	wTrackLength = 2.0
	wTrackID     = 5.0
	wTrackISRC   = 1.0
)

// Track length: no penalty within lengthGrace of the listing, the full one at
// lengthGrace + lengthMax off.
const (
	lengthGraceSec = 10.0
	lengthMaxSec   = 30.0
)

// Component names, as stored in a review's breakdown.
const (
	CompAlbumID       = "album_id"
	CompArtist        = "artist"
	CompAlbum         = "album"
	CompTracks        = "tracks"
	CompMissingTracks = "missing_tracks"
	CompUnmatched     = "unmatched_tracks"
	CompDataSource    = "data_source"
	CompMediaCount    = "media_count"
	CompMediaFormat   = "media_format"
	CompYear          = "year"
	CompCountry       = "country"
	CompLabel         = "label"
	CompCatalogNumber = "catalog_number"
	CompAlbumDisambig = "album_disambiguation"
	CompBarcode       = "barcode"
	CompTrackTitle    = "track_title"
	CompTrackArtist   = "track_artist"
	CompTrackIndex    = "track_index"
	CompTrackLength   = "track_length"
	CompTrackID       = "track_id"
	CompTrackISRC     = "isrc"
)

// componentWeights is every component's weight, by name (the table test reads it).
var componentWeights = map[string]float64{
	CompAlbumID: wAlbumID, CompArtist: wArtist, CompAlbum: wAlbum, CompTracks: wTracks,
	CompMissingTracks: wMissingTracks, CompUnmatched: wUnmatched, CompDataSource: wDataSource,
	CompMediaCount: wMediaCount, CompMediaFormat: wMediaFormat, CompYear: wYear, CompCountry: wCountry,
	CompLabel: wLabel, CompCatalogNumber: wCatalogNumber, CompAlbumDisambig: wAlbumDisambig, CompBarcode: wBarcode,
	CompTrackTitle: wTrackTitle, CompTrackArtist: wTrackArtist, CompTrackIndex: wTrackIndex,
	CompTrackLength: wTrackLength, CompTrackID: wTrackID, CompTrackISRC: wTrackISRC,
}

// distance accumulates penalties per component, beets' Distance.
type distance struct {
	order     []string
	penalties map[string][]float64
}

func newDistance() *distance { return &distance{penalties: map[string][]float64{}} }

// add records one penalty (0..1) against a component.
func (d *distance) add(name string, penalty float64) {
	if math.IsNaN(penalty) {
		penalty = 1
	}
	penalty = math.Max(0, math.Min(1, penalty))
	if _, ok := d.penalties[name]; !ok {
		d.order = append(d.order, name)
	}
	d.penalties[name] = append(d.penalties[name], penalty)
}

// addRatio adds n/of, capped at 1.
func (d *distance) addRatio(name string, n, of float64) {
	if of <= 0 {
		d.add(name, 1)
		return
	}
	d.add(name, n/of)
}

// addBool adds a full penalty when bad, none otherwise.
func (d *distance) addBool(name string, bad bool) {
	if bad {
		d.add(name, 1)
	} else {
		d.add(name, 0)
	}
}

// raw and max are the weighted penalty sum and the largest it could be, leaving out the
// named components.
func (d *distance) sums(skip ...string) (raw, max float64) {
	for name, ps := range d.penalties {
		if contains(skip, name) {
			continue
		}
		w := componentWeights[name]
		for _, p := range ps {
			raw += p * w
			max += w
		}
	}
	return raw, max
}

// value is the distance, 0..1. With nothing compared at all it is 0, as in beets; callers
// that need "nothing said it fits" check known().
func (d *distance) value(skip ...string) float64 {
	raw, max := d.sums(skip...)
	if max == 0 {
		return 0
	}
	return raw / max
}

// known reports whether any component was compared.
func (d *distance) known() bool { return len(d.order) > 0 }

// Component is one field's part of a distance, for the breakdown Reviews shows.
type Component struct {
	Name    string  `json:"name"`
	Weight  float64 `json:"weight"`
	Penalty float64 `json:"penalty"` // the summed penalties (a component can be added per track)
	Count   int     `json:"count"`   // how many times it was added
	Share   float64 `json:"share"`   // penalty × weight over the whole distance's maximum
}

// components lists every component with its share of the distance, in the order added.
func (d *distance) components() []Component {
	_, max := d.sums()
	out := make([]Component, 0, len(d.order))
	for _, name := range d.order {
		ps := d.penalties[name]
		sum := 0.0
		for _, p := range ps {
			sum += p
		}
		c := Component{Name: name, Weight: componentWeights[name], Penalty: round4(sum), Count: len(ps)}
		if max > 0 {
			c.Share = round4(sum * c.Weight / max)
		}
		out = append(out, c)
	}
	return out
}

func round4(f float64) float64 { return math.Round(f*10000) / 10000 }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// variousArtistsMBID is MusicBrainz's "Various Artists".
const variousArtistsMBID = "89ad4ac3-39f7-470e-963a-56509c546377"

// isVariousArtists reports whether a name is a "Various Artists" placeholder.
func isVariousArtists(s string) bool {
	switch compactFold(s) {
	case "variousartists", "various", "va", "variousartist":
		return true
	}
	return false
}

// trackDistance compares one file with one track of a candidate edition. notes say in
// words what didn't fit ("length off by 45 s"). A file and track with nothing to compare
// are as far apart as they can be: nothing says the file is that track.
func trackDistance(f *MatchFile, t *candTrack, va bool) (float64, *distance, []string) {
	d := newDistance()
	var notes []string
	if f.DurationMs > 0 && t.LengthMs > 0 {
		off := math.Abs(float64(f.DurationMs-t.LengthMs)) / 1000
		d.addRatio(CompTrackLength, off-lengthGraceSec, lengthMaxSec)
		if off > lengthGraceSec {
			notes = append(notes, fmt.Sprintf("length off by %d s", int(math.Round(off))))
		}
	}
	if title := f.title(); title != "" {
		sd := stringDist(title, t.Title)
		d.add(CompTrackTitle, sd)
		if sd >= 0.2 {
			notes = append(notes, fmt.Sprintf("title %q differs from %q", title, t.Title))
		}
	}
	if !va && f.Tags.Artist != "" && t.Credit != "" && !isVariousArtists(f.Tags.Artist) {
		sd := artistDist(f.Tags.Artist, t.Credit, t.creditAliases)
		d.add(CompTrackArtist, sd)
		if sd >= 0.3 {
			notes = append(notes, fmt.Sprintf("artist %q differs from %q", f.Tags.Artist, t.Credit))
		}
	}
	if disc, num := f.number(); num > 0 {
		ok := num == t.Index || (num == t.Position && (disc == 0 || disc == t.Disc))
		d.addBool(CompTrackIndex, !ok)
		if !ok {
			notes = append(notes, fmt.Sprintf("numbered %s but it is track %s", numLabel(disc, num), numLabel(t.Disc, t.Position)))
		}
	}
	switch {
	case f.Tags.RecordingMBID != "" && t.RecordingMBID != "":
		// AcoustID often names several recordings for one sound (the same take on an
		// album and a compilation): any of them will do.
		bad := !strings.EqualFold(f.Tags.RecordingMBID, t.RecordingMBID) && !t.hasAlias(f.Tags.RecordingMBID) &&
			!containsFold(f.Fingerprint, t.RecordingMBID)
		d.addBool(CompTrackID, bad)
		if bad {
			notes = append(notes, "its MusicBrainz recording id is a different recording")
		}
	case len(f.Fingerprint) > 0 && t.RecordingMBID != "":
		// No id in the tags: what AcoustID heard stands in for one.
		bad := !containsFold(f.Fingerprint, t.RecordingMBID) && !anyAlias(f.Fingerprint, t.aliases)
		d.addBool(CompTrackID, bad)
		if bad {
			notes = append(notes, "AcoustID hears a different recording")
		}
	}
	if f.Tags.ISRC != "" && len(t.ISRCs) > 0 {
		bad := !containsFold(t.ISRCs, f.Tags.ISRC)
		d.addBool(CompTrackISRC, bad)
		if bad {
			notes = append(notes, "its ISRC belongs to another recording")
		}
	}
	if !d.known() {
		return 1, d, []string{"nothing in the file to compare"}
	}
	return d.value(), d, notes
}

// artistDist is how far a tag's artist is from a credit: the credit's own distance, or an
// alias's when one is closer (MUS-38: "Utada Hikaru" tags on 宇多田ヒカル's album fit).
func artistDist(tag, credit string, aliases []string) float64 {
	best := stringDist(tag, credit)
	for _, a := range aliases {
		if best == 0 {
			break
		}
		best = math.Min(best, stringDist(tag, a))
	}
	return best
}

func numLabel(disc, n int) string {
	if disc > 0 {
		return fmt.Sprintf("%d-%d", disc, n)
	}
	return strconv.Itoa(n)
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}

// nowYear is the current year (a seam for tests).
var nowYear = func() int { return time.Now().Year() }

// digitsOnly keeps the digits of a barcode, without leading zeros (a UPC and its EAN-13
// form differ only by one).
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(b.String(), "0")
}

// normCatalog compares catalogue numbers without case, spaces or dashes.
func normCatalog(s string) string {
	return strings.NewReplacer(" ", "", "-", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(s)))
}
