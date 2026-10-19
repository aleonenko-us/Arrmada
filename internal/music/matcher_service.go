package music

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The service side of the matcher (MUS-31): its settings, and the candidate editions it
// weighs, read from the library (and from MusicBrainz, through the cache, when an
// edition's tracks aren't stored yet).

// Settings the matcher reads (Settings → Naming & metadata → Music matching).
const (
	KeyMatchStrong     = "music_match_strong"
	KeyMatchExpected   = "music_match_expected"
	KeyMatchReview     = "music_match_review"
	KeyMatchTrackMax   = "music_match_track_max"
	KeyMatchGap        = "music_match_gap"
	KeyMatchPartialMin = "music_match_partial_min"
)

// MatchSettings are the matcher's thresholds in effect: each saved one that reads as a
// number, else its default. A saved set that doesn't hold together (Validate) falls back
// to the defaults whole, so a half-edited set can never loosen the matcher by accident.
func (s *Service) MatchSettings() MatchSettings {
	m := DefaultMatchSettings()
	for key, p := range map[string]*float64{
		KeyMatchStrong: &m.Strong, KeyMatchExpected: &m.Expected, KeyMatchReview: &m.Review,
		KeyMatchTrackMax: &m.TrackMax, KeyMatchGap: &m.Gap, KeyMatchPartialMin: &m.PartialMin,
	} {
		if v, ok := s.setting(key); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				*p = f
			}
		}
	}
	if m.Validate() != nil {
		return DefaultMatchSettings()
	}
	return m
}

// maxMatchCandidates bounds the editions one match weighs, all albums together: each
// unlisted one costs a MusicBrainz call (1.1 s) the first time.
const maxMatchCandidates = 10

// MatchCandidates gathers the editions a download of fileCount files might be, from the
// given albums (in order: the album it was grabbed for, then the ones its tags or name
// point at): each album's target first, then its other acceptable editions, one per
// track count first (the closest to fileCount leading), so different editions get a
// look before near-identical pressings do. Track lists are fetched as needed.
func (s *Service) MatchCandidates(ctx context.Context, albumIDs []int64, fileCount int) ([]MatchCandidate, error) {
	ep, hasEP := s.meta.(metadata.EditionProvider)
	var out []MatchCandidate
	seen := map[int64]bool{}
	for _, id := range albumIDs {
		if id <= 0 || seen[id] || len(out) >= maxMatchCandidates {
			continue
		}
		seen[id] = true
		al, err := s.repo.GetAlbum(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if s.meta != nil {
			if err := s.EnsureTracks(ctx, al); err != nil {
				s.log.Warn("music: couldn't list the album's editions", "album", al.Title, "err", err)
			}
			if al, err = s.repo.GetAlbum(ctx, id); err != nil {
				return nil, err
			}
		}
		eds, err := s.repo.Editions(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, e := range candidateEditions(eds, al, fileCount, maxMatchCandidates-len(out)) {
			if !e.TracksFetched && e.MBID != "" {
				if !hasEP {
					continue
				}
				if err := s.fetchEditionTracks(ctx, ep, e); err != nil {
					s.log.Warn("music: couldn't read an edition's tracks", "album", al.Title, "edition", e.MBID, "err", err)
					continue
				}
			}
			c, err := s.candidate(ctx, al, e.ID)
			if err != nil {
				return nil, err
			}
			if len(c.trackList()) > 0 {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// candidateEditions orders an album's editions for matching and keeps up to limit.
func candidateEditions(eds []Edition, al Album, files, limit int) []Edition {
	if limit <= 0 {
		return nil
	}
	var out, rest []Edition
	for _, e := range eds {
		switch {
		case e.ID == al.TargetEditionID:
			out = append(out, e)
		case e.Legacy || !EditionAcceptable(e, false):
		default:
			rest = append(rest, e)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		di, dj := absInt(rest[i].TrackCount-files), absInt(rest[j].TrackCount-files)
		if di != dj {
			return di < dj
		}
		return dateBefore(rest[i].ReleaseDate, rest[j].ReleaseDate) < 0
	})
	counts := map[int]bool{}
	for _, e := range out {
		counts[e.TrackCount] = true
	}
	var later []Edition
	for _, e := range rest {
		if counts[e.TrackCount] {
			later = append(later, e)
			continue
		}
		counts[e.TrackCount] = true
		out = append(out, e)
	}
	out = append(out, later...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// candidate loads one edition in full as a match candidate.
func (s *Service) candidate(ctx context.Context, al Album, releaseID int64) (MatchCandidate, error) {
	ed, err := s.repo.Edition(ctx, releaseID)
	if err != nil {
		return MatchCandidate{}, err
	}
	creditID := ed.CreditID
	if creditID == 0 {
		creditID = al.CreditID
	}
	ids := []int64{creditID}
	for _, m := range ed.Media {
		for _, t := range m.Tracks {
			if t.CreditID != 0 && t.CreditID != creditID {
				ids = append(ids, t.CreditID)
			}
		}
	}
	credits, err := s.repo.Credits(ctx, ids)
	if err != nil {
		return MatchCandidate{}, err
	}
	c := MatchCandidate{
		AlbumID: al.ID, AlbumTitle: al.Title, AlbumYear: al.Year, Edition: ed,
		Target: ed.ID == al.TargetEditionID, Pinned: al.EditionPinned,
		TrackCredits: map[int64]string{},
	}
	if cr, ok := credits[creditID]; ok {
		c.Credit = cr.Display
		for _, n := range cr.Names {
			if n.ArtistMBID == variousArtistsMBID {
				c.VariousArtists = true
			}
		}
	} else {
		c.Credit = al.ArtistCredit
	}
	if isVariousArtists(c.Credit) {
		c.VariousArtists = true
	}
	if !c.VariousArtists {
		if ix, err := s.NameIndex(ctx); err == nil {
			c.ArtistAliases = ix.Names(al.ArtistID, "").Aliases
		}
	}
	for _, m := range ed.Media {
		for _, t := range m.Tracks {
			if cr, ok := credits[t.CreditID]; ok && t.CreditID != creditID {
				c.TrackCredits[t.ID] = cr.Display
			}
		}
	}
	isrcs, old, err := s.repo.recordingIDs(ctx, releaseID)
	if err != nil {
		return MatchCandidate{}, err
	}
	c.ISRCs = isrcs
	if len(old) > 0 {
		c.RecordingAliases = map[string]string{}
		for _, m := range ed.Media {
			for _, t := range m.Tracks {
				for _, o := range old[t.RecordingID] {
					c.RecordingAliases[o] = t.RecordingMBID
				}
			}
		}
	}
	return c, nil
}

// TargetCandidates is the album's target edition alone as a match candidate (after
// making sure it has a listing): for the library scan, which can't afford a MusicBrainz
// call per edition per album.
func (s *Service) TargetCandidates(ctx context.Context, albumID int64) ([]MatchCandidate, error) {
	al, err := s.repo.GetAlbum(ctx, albumID)
	if err != nil {
		return nil, err
	}
	if s.meta != nil && al.EditionTracks == 0 {
		// The album has no listing at all: fetch the one listing, as the scan always did.
		// No editions are listed and nothing is re-keyed from here: a scan of a big
		// library can't afford MusicBrainz calls per album.
		if err := s.ensureListing(ctx, al); err != nil {
			return nil, err
		}
		if al, err = s.repo.GetAlbum(ctx, albumID); err != nil {
			return nil, err
		}
	}
	if al.TargetEditionID == 0 {
		return nil, nil
	}
	c, err := s.candidate(ctx, al, al.TargetEditionID)
	if err != nil || len(c.trackList()) == 0 {
		return nil, err
	}
	c.Target = true
	return []MatchCandidate{c}, nil
}

// EditionCandidate is one edition of an album as a match candidate, whatever its status:
// the edition a person chose in Reviews. Its tracks are fetched first when they aren't
// stored. ErrNotFound when it isn't the album's; ErrEditionNotListed when its tracks
// can't be had.
func (s *Service) EditionCandidate(ctx context.Context, albumID, releaseID int64) (MatchCandidate, error) {
	al, err := s.repo.GetAlbum(ctx, albumID)
	if err != nil {
		return MatchCandidate{}, err
	}
	ed, err := s.repo.Edition(ctx, releaseID)
	if err != nil {
		return MatchCandidate{}, err
	}
	if ed.AlbumID != albumID {
		return MatchCandidate{}, ErrNotFound
	}
	if !ed.TracksFetched {
		ep, ok := s.meta.(metadata.EditionProvider)
		if !ok {
			return MatchCandidate{}, ErrEditionNotListed
		}
		if err := s.fetchEditionTracks(ctx, ep, ed); err != nil {
			return MatchCandidate{}, err
		}
	}
	c, err := s.candidate(ctx, al, releaseID)
	if err != nil {
		return MatchCandidate{}, err
	}
	if len(c.trackList()) == 0 {
		return MatchCandidate{}, ErrEditionNotListed
	}
	return c, nil
}

// AlbumsWithRecordings lists the library albums any of whose stored editions carry one
// of the recordings (by MBID): where an AcoustID answer points.
func (s *Service) AlbumsWithRecordings(ctx context.Context, mbids []string) ([]int64, error) {
	return s.repo.albumsWithRecordings(ctx, mbids)
}

// AlbumByMBID finds a library album by its release-group MBID (0: none).
func (s *Service) AlbumByMBID(ctx context.Context, mbid string) (int64, error) {
	return s.repo.albumByMBID(ctx, mbid)
}

// AlbumOfRelease finds the library album an edition MBID belongs to: stored editions
// first, then (when MusicBrainz can say) the release's group. 0 when it is no library
// album's.
func (s *Service) AlbumOfRelease(ctx context.Context, releaseMBID string) (int64, error) {
	if id, err := s.repo.albumOfRelease(ctx, releaseMBID); err != nil || id > 0 {
		return id, err
	}
	ep, ok := s.meta.(metadata.EditionProvider)
	if !ok {
		return 0, nil
	}
	d, err := ep.ReleaseTracks(ctx, releaseMBID)
	if err != nil || d.ReleaseGroupMBID == "" {
		return 0, nil // unknown: the other candidates still count
	}
	return s.repo.albumByMBID(ctx, d.ReleaseGroupMBID)
}

// AlbumWanted lists the tracks of an album's target edition that are wanted and have no
// file, for the matcher's partial rule.
func (s *Service) AlbumWanted(ctx context.Context, albumID int64) ([]WantedTrack, error) {
	al, err := s.repo.GetAlbum(ctx, albumID)
	if err != nil {
		return nil, err
	}
	tracks, err := s.repo.TracksFor(ctx, albumID)
	if err != nil {
		return nil, err
	}
	var out []WantedTrack
	for _, t := range tracks {
		if !t.HasFile && (t.Monitored || !al.Monitored) {
			out = append(out, WantedTrack{AlbumID: albumID, TrackID: t.ID, RecordingID: t.RecordingID, Title: t.Title})
		}
	}
	return out, nil
}
