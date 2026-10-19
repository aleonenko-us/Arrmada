package music

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// Placement (MUS-33, MUS-34, MUS-36): after the matcher decides which edition a group of
// files is and which file is which track, placement decides how each file lands: its
// house tags, its name, its extras. This file is the boundary between the two.

// MatchResult is what the matcher decided for one group of files: the edition they are,
// each file's release track, the files it couldn't place, and why (the score breakdown,
// for the album's history and Reviews).
type MatchResult struct {
	AlbumID     int64
	EditionID   int64 // the edition the files were matched to (0 = the album's target)
	Assignments []Assignment
	Unmatched   []AudioFile
	Score       *ScoreBreakdown
	// ContentRoot is the folder the files were found in (the download's folder, or one
	// album's folder inside a discography). Extras only ever come from inside it; "" (or a
	// single-file download) brings none, so a download saved straight into the downloads
	// folder never pulls in its neighbours' files.
	ContentRoot string
}

// Assignment is one file placed on one release track.
type Assignment struct {
	File           AudioFile
	ReleaseTrackID int64
}

// ScoreBreakdown is the matcher's distance and its parts (MUS-31); placement only carries
// it along.
type ScoreBreakdown struct {
	Distance   float64            `json:"distance"`
	Components map[string]float64 `json:"components,omitempty"`
	Notes      []string           `json:"notes,omitempty"`
}

// MatchFromTracks adapts today's track matching (MatchTracks: release track id → file)
// to a MatchResult against the album's target edition.
func MatchFromTracks(al Album, matched map[int64]AudioFile, unmatched []AudioFile, contentRoot string) MatchResult {
	r := MatchResult{AlbumID: al.ID, EditionID: al.TargetEditionID, Unmatched: unmatched, ContentRoot: contentRoot}
	for id, f := range matched {
		r.Assignments = append(r.Assignments, Assignment{File: f, ReleaseTrackID: id})
	}
	return r
}

// VariousArtistsMBID is MusicBrainz's "Various Artists" special artist.
const VariousArtistsMBID = "89ad4ac3-39f7-470e-963a-56509c546377"

// IsVariousArtists reports whether a credit is "Various Artists": a compilation.
func IsVariousArtists(c Credit) bool {
	if len(c.Names) == 1 && c.Names[0].ArtistMBID == VariousArtistsMBID {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(c.Display), "Various Artists")
}

// TagContext is everything an album's house tags and names are made from: the album, the
// library artist it's filed under, one edition in full, its credits (with the artists'
// sort names) and its recordings' ISRCs. Loaded once per album.
type TagContext struct {
	Album   Album
	Artist  Artist
	Edition Edition
	Credits map[int64]Credit
	// Sorts are artists' sort names by artist id ("Beatles, The").
	Sorts map[int64]string
	// ISRCs are by recording id.
	ISRCs map[int64][]string

	pos map[int64][2]int // release track id → (medium index, track index)
}

// TagContext loads an album's tag context against editionID (0 = its target edition).
func (s *Service) TagContext(ctx context.Context, albumID, editionID int64) (TagContext, error) {
	return s.repo.tagContext(ctx, albumID, editionID)
}

func (r *Repo) tagContext(ctx context.Context, albumID, editionID int64) (TagContext, error) {
	var tc TagContext
	al, err := r.GetAlbum(ctx, albumID)
	if err != nil {
		return tc, err
	}
	tc.Album = al
	if tc.Artist, err = r.GetArtist(ctx, al.ArtistID); err != nil {
		return tc, err
	}
	if editionID == 0 {
		editionID = al.TargetEditionID
	}
	if editionID == 0 {
		return tc, errors.New("music: the album has no edition to tag from yet")
	}
	if tc.Edition, err = r.Edition(ctx, editionID); err != nil {
		return tc, err
	}
	if tc.Edition.AlbumID != albumID {
		return tc, errors.New("music: that edition belongs to another album")
	}
	ids := []int64{al.CreditID, tc.Edition.CreditID}
	var recs []int64
	tc.pos = map[int64][2]int{}
	for mi, m := range tc.Edition.Media {
		for ti, t := range m.Tracks {
			ids = append(ids, t.CreditID)
			recs = append(recs, t.RecordingID)
			tc.pos[t.ID] = [2]int{mi, ti}
		}
	}
	if tc.Credits, err = r.Credits(ctx, ids); err != nil {
		return tc, err
	}
	var artists []int64
	for _, c := range tc.Credits {
		for _, n := range c.Names {
			artists = append(artists, n.ArtistID)
		}
	}
	if tc.Sorts, err = r.sortNames(ctx, artists); err != nil {
		return tc, err
	}
	if tc.ISRCs, err = r.recordingISRCs(ctx, recs); err != nil {
		return tc, err
	}
	return tc, nil
}

// sortNames reads artists' sort names.
func (r *Repo) sortNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	args, in := idArgs(ids)
	if len(args) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, sort_name FROM artists WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var sort sql.NullString
		if err := rows.Scan(&id, &sort); err != nil {
			return nil, err
		}
		out[id] = strings.TrimSpace(sort.String)
	}
	return out, rows.Err()
}

// recordingISRCs reads the ISRCs stored for recordings.
func (r *Repo) recordingISRCs(ctx context.Context, ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	args, in := idArgs(ids)
	if len(args) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT entity_id, value FROM music_external_ids
		 WHERE entity_type = 'recording' AND source = 'isrc' AND entity_id IN (`+in+`)
		 ORDER BY entity_id, value`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = append(out[id], v)
	}
	return out, rows.Err()
}

// idArgs dedupes positive ids into query arguments and their placeholders.
func idArgs(ids []int64) ([]any, string) {
	seen := map[int64]bool{}
	var args []any
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			args = append(args, id)
		}
	}
	if len(args) == 0 {
		return nil, ""
	}
	return args, "?" + strings.Repeat(", ?", len(args)-1)
}

// Track finds a release track of the edition, with its disc.
func (tc TagContext) Track(releaseTrackID int64) (Medium, ReleaseTrack, bool) {
	p, ok := tc.pos[releaseTrackID]
	if !ok {
		return Medium{}, ReleaseTrack{}, false
	}
	m := tc.Edition.Media[p[0]]
	return m, m.Tracks[p[1]], true
}

// releaseCredit is the edition's credit, else the album's.
func (tc TagContext) releaseCredit() Credit {
	if c, ok := tc.Credits[tc.Edition.CreditID]; ok && tc.Edition.CreditID > 0 {
		return c
	}
	return tc.Credits[tc.Album.CreditID]
}

// trackCredit is a track's own credit, else the edition's.
func (tc TagContext) trackCredit(t ReleaseTrack) Credit {
	if c, ok := tc.Credits[t.CreditID]; ok && t.CreditID > 0 {
		return c
	}
	return tc.releaseCredit()
}

// Compilation reports whether the album is a Various Artists release.
func (tc TagContext) Compilation() bool { return IsVariousArtists(tc.releaseCredit()) }

// discCount is the edition's number of discs.
func (tc TagContext) discCount() int {
	if n := len(tc.Edition.Media); n > 0 {
		return n
	}
	return tc.Edition.DiscCount
}

// FileFacts are what was measured in a file, for names with {Quality} and friends.
type FileFacts struct {
	Quality    Quality
	Codec      string
	BitDepth   int
	SampleRate int
}

// yearOf reads the year from a MusicBrainz date ("1997-05-21", "1997").
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil || y <= 0 {
		return 0
	}
	return y
}
