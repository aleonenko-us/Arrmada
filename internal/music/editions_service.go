package music

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// Settings the edition picker reads (Settings → Naming & metadata → Music matching).
const (
	KeyEditionPolicy = "music_edition_policy"
	// KeyEditionCountries / KeyEditionMedia are the preferred policy's ordered lists,
	// comma-separated ("GB,US,XW"; "Digital Media,CD").
	KeyEditionCountries = "music_edition_countries"
	KeyEditionMedia     = "music_edition_media"
)

// DefaultEditionMedia is the preferred policy's media order when none is saved.
var DefaultEditionMedia = []string{"Digital Media", "CD"}

// SetSettingsLookup gives the service the saved settings (settings.Service.Lookup).
func (s *Service) SetSettingsLookup(f func(key string) (string, bool)) { s.lookup = f }

func (s *Service) setting(key string) (string, bool) {
	if s.lookup == nil {
		return "", false
	}
	v, ok := s.lookup(key)
	return strings.TrimSpace(v), ok && strings.TrimSpace(v) != ""
}

// EditionPolicy is the policy in effect (original unless another is saved).
func (s *Service) EditionPolicy() EditionPolicy {
	p := EditionPolicy{Mode: PolicyOriginal}
	if v, ok := s.setting(KeyEditionPolicy); ok && ValidPolicy(v) {
		p.Mode = v
	}
	p.Countries = SplitList(s.settingOr(KeyEditionCountries, ""))
	p.Media = SplitList(s.settingOr(KeyEditionMedia, strings.Join(DefaultEditionMedia, ",")))
	return p
}

func (s *Service) settingOr(key, def string) string {
	if v, ok := s.setting(key); ok {
		return v
	}
	return def
}

// SplitList reads a comma-separated list, trimmed, empty entries dropped.
func SplitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// editionsRecheck is how long an album's list of editions is trusted before it's listed
// again (from the cache, which refreshes itself; this only re-reads it into the tables).
const editionsRecheck = 30 * 24 * time.Hour

// editionsRetry is how soon a listing that failed (MusicBrainz down) is tried again, for
// an album that already has a usable target edition.
const editionsRetry = 24 * time.Hour

// EnsureEditions lists every edition of an album (one browse call) when it hasn't been
// lately, and keeps a target edition with its tracks:
//   - no target yet: the policy's pick;
//   - the migration's legacy edition: re-keyed to the policy's pick, its file rows moved
//     to the matching tracks (files that fit none stay on the legacy edition, which is
//     listed while it holds any);
//   - a target with no files, not pinned, that isn't the policy's pick: the pick;
//   - otherwise the target stays (pinned, or holding files the matcher placed on it).
//
// A failed or empty answer changes nothing.
func (s *Service) EnsureEditions(ctx context.Context, al Album) error {
	if ep, ok := s.meta.(metadata.EditionProvider); ok {
		return s.ensureEditions(ctx, ep, al)
	}
	return s.ensureListing(ctx, al)
}

func (s *Service) ensureEditions(ctx context.Context, ep metadata.EditionProvider, al Album) error {
	st, err := s.repo.editionState(ctx, al.ID)
	if err != nil {
		return err
	}
	listed := !st.CheckedAt.IsZero() && time.Since(st.CheckedAt) < editionsRecheck
	if listed && st.HasTarget && st.TargetFetched {
		// The common case: nothing to do, one query. A legacy target that is still the
		// target was tried when the editions were listed and kept (rekeyLegacy); it is
		// tried again only with the next listing, not on every look.
		return nil
	}
	if !listed {
		rels, err := ep.AlbumReleases(ctx, al.MBID)
		if err != nil {
			if st.HasTarget && st.TargetFetched {
				// MusicBrainz is down: work with what's stored, and ask again in a day
				// rather than on every page open (each would wait out the retries).
				s.log.Warn("music: couldn't list the album's editions; keeping what we have", "album", al.Title, "err", err)
				_ = s.repo.markEditionsChecked(ctx, al.ID, time.Now().Add(editionsRetry-editionsRecheck))
				return nil
			}
			return err
		}
		if len(rels) > 0 {
			sums := make([]EditionSummary, 0, len(rels))
			for _, r := range rels {
				sums = append(sums, summaryFrom(r))
			}
			if err := s.repo.StoreEditionSummaries(ctx, al.ID, sums); err != nil {
				return err
			}
		}
	}
	fresh, err := s.repo.GetAlbum(ctx, al.ID)
	if err != nil {
		return err
	}
	eds, err := s.repo.Editions(ctx, al.ID)
	if err != nil {
		return err
	}
	var target *Edition
	for i := range eds {
		if eds[i].ID == fresh.TargetEditionID {
			target = &eds[i]
		}
	}
	if fresh.EditionPinned && target != nil {
		if !target.TracksFetched && target.MBID != "" {
			return s.fetchEditionTracks(ctx, ep, *target)
		}
		return nil
	}
	pick, ok := PickEdition(eds, s.EditionPolicy())
	if !ok {
		if target != nil && !target.TracksFetched && target.MBID != "" {
			return s.fetchEditionTracks(ctx, ep, *target)
		}
		return nil // nothing acceptable listed: keep what there is
	}
	switch {
	case target == nil:
	case target.Legacy:
		return s.rekeyLegacy(ctx, ep, fresh, target.ID, eds)
	case target.ID != pick.ID:
		counts, err := s.repo.EditionFileCounts(ctx, al.ID)
		if err != nil {
			return err
		}
		if counts[target.ID] > 0 {
			pick = *target // it holds files: the matcher (or a person) chose it
		}
	default:
	}
	if !pick.TracksFetched {
		if err := s.fetchEditionTracks(ctx, ep, pick); err != nil {
			return err
		}
	}
	if target != nil && target.ID == pick.ID {
		return nil
	}
	res, err := s.repo.SwitchEdition(ctx, al.ID, pick.ID, false)
	if err != nil {
		return err
	}
	if target != nil {
		label := EditionLabel(pick)
		s.repo.AddEvent(ctx, fresh.ArtistID, "edition", fmt.Sprintf("%q follows the edition policy: the %s edition", fresh.Title, label))
		s.log.Info("music: target edition set", "album", fresh.Title, "edition", label, "moved", res.Moved, "kept", res.Kept)
	}
	return nil
}

// Bounds on a legacy album's re-key, each untried edition costing a MusicBrainz call: how
// many editions with the old listing's track count are read looking for the release it
// came from, and how many more the policy offers after that.
const (
	rekeyOriginTries = 3
	rekeyPolicyTries = 2
)

// rekeyLegacy moves an album off the migration's legacy edition onto a real MusicBrainz
// edition, once per listing of its editions (ensureEditions only calls it then):
//
//  1. The release the old listing was read from, first. The legacy tracks still carry
//     that release's track ids, so an edition whose tracks have every one of them is the
//     old listing itself. The old code read the earliest dated official release, so
//     editions with the same track count are tried earliest first.
//  2. Failing that, the policy's picks.
//
// RekeyEdition only switches when every file finds its track and the album reads no less
// complete than before (and a track no file covers isn't newly wanted). An album that fits
// nothing keeps its old listing as it was: the policy's same-day deluxe never turns a
// complete album into one the sweep grabs again.
func (s *Service) rekeyLegacy(ctx context.Context, ep metadata.EditionProvider, al Album, legacyID int64, eds []Edition) error {
	legacy, err := s.repo.Edition(ctx, legacyID)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, t := range audioTracks(legacy) {
		if t.MBID != "" {
			ids[strings.ToLower(t.MBID)] = true
		}
	}
	tried := map[int64]bool{}
	try := func(e Edition) (bool, error) {
		tried[e.ID] = true
		if !e.TracksFetched {
			if err := s.fetchEditionTracks(ctx, ep, e); err != nil {
				s.log.Warn("music: couldn't read an edition's tracks", "album", al.Title, "edition", e.MBID, "err", err)
				return false, nil
			}
		}
		res, err := s.repo.RekeyEdition(ctx, al.ID, e.ID)
		if errors.Is(err, ErrWouldStrand) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		label := EditionLabel(e)
		s.repo.AddEvent(ctx, al.ArtistID, "edition", fmt.Sprintf("%q is now the %s edition from MusicBrainz (%s)",
			al.Title, label, movedWords(res)))
		s.log.Info("music: re-keyed the upgrade's listing", "album", al.Title, "edition", label, "moved", res.Moved)
		return true, nil
	}

	if len(ids) > 0 {
		var same []Edition
		for _, e := range eds {
			if !e.Legacy && e.MBID != "" && e.TrackCount == legacy.TrackCount {
				same = append(same, e)
			}
		}
		sort.SliceStable(same, func(i, j int) bool { return dateBefore(same[i].ReleaseDate, same[j].ReleaseDate) < 0 })
		for i, e := range same {
			if i == rekeyOriginTries {
				break
			}
			if !e.TracksFetched {
				if err := s.fetchEditionTracks(ctx, ep, e); err != nil {
					s.log.Warn("music: couldn't read an edition's tracks", "album", al.Title, "edition", e.MBID, "err", err)
					continue
				}
			}
			full, err := s.repo.Edition(ctx, e.ID)
			if err != nil {
				return err
			}
			has := map[string]bool{}
			for _, t := range audioTracks(full) {
				has[strings.ToLower(t.MBID)] = true
			}
			origin := true
			for id := range ids {
				if !has[id] {
					origin = false
					break
				}
			}
			if !origin {
				continue
			}
			full.TracksFetched = true
			if ok, err := try(full); ok || err != nil {
				return err
			}
		}
	}

	pol := s.EditionPolicy()
	var left []Edition
	for _, e := range eds {
		if !tried[e.ID] {
			left = append(left, e)
		}
	}
	for n := 0; n < rekeyPolicyTries; n++ {
		pick, ok := PickEdition(left, pol)
		if !ok {
			break
		}
		for i := range left {
			if left[i].ID == pick.ID {
				left = append(left[:i], left[i+1:]...)
				break
			}
		}
		if ok, err := try(pick); ok || err != nil {
			return err
		}
	}
	s.log.Info("music: kept the upgrade's listing: no MusicBrainz edition holds its files as they are", "album", al.Title)
	return nil
}

// movedWords says what a switch did to the album's files.
func movedWords(r SwitchResult) string {
	switch {
	case r.Moved == 0 && r.Kept == 0:
		return "no files to move"
	case r.Kept == 0:
		return fmt.Sprintf("%d file(s) moved to its tracks", r.Moved)
	}
	return fmt.Sprintf("%d file(s) moved to its tracks, %d that fit none kept as extras", r.Moved, r.Kept)
}

// summaryFrom turns a browse answer into what the repo stores.
func summaryFrom(r metadata.ReleaseSummary) EditionSummary {
	s := EditionSummary{Edition: Edition{
		MBID: r.MBID, Title: r.Title, Status: r.Status, ReleaseDate: r.Date, Country: r.Country, Label: r.Label,
		CatalogNumber: r.CatalogNumber, Barcode: r.Barcode, Packaging: r.Packaging, Disambiguation: r.Disambiguation,
		MediaSummary: r.MediaSummary(),
	}, Credit: creditInputs(r.Credit)}
	for _, m := range r.Media {
		s.Media = append(s.Media, Medium{Position: m.Position, Format: m.Format, Title: m.Title, TrackCount: m.TrackCount})
	}
	return s
}

func creditInputs(parts []metadata.CreditPart) []CreditInput {
	out := make([]CreditInput, 0, len(parts))
	for _, p := range parts {
		out = append(out, CreditInput{ArtistMBID: p.ArtistMBID, ArtistName: p.ArtistName, SortName: p.SortName, Name: p.Name, JoinPhrase: p.JoinPhrase})
	}
	return out
}

// fetchEditionTracks reads one edition's full track list from MusicBrainz (through the
// cache) and stores it. A track on a video disc (a deluxe's bonus DVD) is stored
// unmonitored: no download holds it as audio.
func (s *Service) fetchEditionTracks(ctx context.Context, ep metadata.EditionProvider, ed Edition) error {
	if ed.MBID == "" {
		return ErrEditionNotListed
	}
	d, err := ep.ReleaseTracks(ctx, ed.MBID)
	if err != nil {
		return err
	}
	sum := summaryFrom(d.ReleaseSummary)
	var tracks []EditionTrack
	for _, m := range d.Media {
		for _, t := range m.Tracks {
			tracks = append(tracks, EditionTrack{
				Disc: discOf(m.Position), DiscFormat: m.Format, DiscTitle: m.Title, Position: t.Position, Number: t.Number,
				Title: t.Title, MBID: t.MBID, RecordingMBID: t.RecordingMBID, LengthMs: t.LengthMs,
				Credit: creditInputs(t.Credit), ISRCs: t.ISRCs, Unmonitored: IgnoredMedium(m.Format),
			})
		}
	}
	if len(tracks) == 0 {
		return ErrEditionNotListed
	}
	return s.repo.StoreEditionTracks(ctx, ed.ID, sum, tracks)
}

// EditionView is one edition in the album page's picker.
type EditionView struct {
	Edition
	// Words describes it: "GB CD 1997 · 12 tracks · deluxe edition".
	Words      string `json:"words"`
	Acceptable bool   `json:"acceptable"`
	// SameAsTarget marks an edition with the target's tracks (equivalent): a download of
	// it satisfies the album as it is. Known only when both track lists are stored.
	SameAsTarget bool `json:"same_as_target,omitempty"`
	Files        int  `json:"files"`
}

// AlbumEditions lists an album's editions for the picker, the target first. The
// migration's legacy edition is listed only while it is the target or still holds files.
func (s *Service) AlbumEditions(ctx context.Context, albumID int64) ([]EditionView, bool, error) {
	al, err := s.repo.GetAlbum(ctx, albumID)
	if err != nil {
		return nil, false, err
	}
	// Opening the picker is asking for the editions: list them (and re-key a migrated
	// album) now if that hasn't happened lately. The album page itself never does.
	if s.meta != nil {
		if err := s.EnsureTracks(ctx, al); err != nil {
			s.log.Warn("music: couldn't list the album's editions", "album", al.Title, "err", err)
		}
		if al, err = s.repo.GetAlbum(ctx, albumID); err != nil {
			return nil, false, err
		}
	}
	eds, err := s.repo.Editions(ctx, albumID)
	if err != nil {
		return nil, false, err
	}
	counts, err := s.repo.EditionFileCounts(ctx, albumID)
	if err != nil {
		return nil, false, err
	}
	var target Edition
	if al.TargetEditionID > 0 {
		if target, err = s.repo.Edition(ctx, al.TargetEditionID); err != nil && !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}
	}
	out := make([]EditionView, 0, len(eds))
	for _, e := range eds {
		if e.Legacy && !e.Target && counts[e.ID] == 0 {
			continue
		}
		v := EditionView{Edition: e, Words: EditionLabel(e), Acceptable: EditionAcceptable(e, e.Target && al.EditionPinned), Files: counts[e.ID]}
		if !e.Target && e.TracksFetched && target.ID > 0 {
			if full, err := s.repo.Edition(ctx, e.ID); err == nil {
				v.SameAsTarget = EditionsEquivalent(full, target)
			}
		}
		out = append(out, v)
	}
	return out, al.EditionPinned, nil
}

// EditionNeedsFetch reports whether choosing an edition would first need its track list
// from MusicBrainz (a request then runs it as a job).
func (s *Service) EditionNeedsFetch(ctx context.Context, albumID, releaseID int64) (bool, error) {
	eds, err := s.repo.Editions(ctx, albumID)
	if err != nil {
		return false, err
	}
	for _, e := range eds {
		if e.ID == releaseID {
			return !e.TracksFetched, nil
		}
	}
	return false, ErrNotFound
}

// SetEdition makes an edition the album's target by hand (pinned or not), fetching its
// track list first when it isn't stored. The album's files move with it (rows only).
func (s *Service) SetEdition(ctx context.Context, albumID, releaseID int64, pinned bool) (SwitchResult, error) {
	needs, err := s.EditionNeedsFetch(ctx, albumID, releaseID)
	if err != nil {
		return SwitchResult{}, err
	}
	ed, err := s.repo.Edition(ctx, releaseID)
	if err != nil {
		return SwitchResult{}, err
	}
	if needs {
		ep, ok := s.meta.(metadata.EditionProvider)
		if !ok {
			return SwitchResult{}, ErrEditionNotListed
		}
		if err := s.fetchEditionTracks(ctx, ep, ed); err != nil {
			return SwitchResult{}, err
		}
		if ed, err = s.repo.Edition(ctx, releaseID); err != nil {
			return SwitchResult{}, err
		}
	}
	return s.switchTo(ctx, albumID, ed, pinned, "by hand")
}

// SwitchEdition moves an album to another of its editions because a download fits it
// (the matcher's switch), keeping the album unpinned.
func (s *Service) SwitchEdition(ctx context.Context, albumID, releaseID int64, why string) (SwitchResult, error) {
	ed, err := s.repo.Edition(ctx, releaseID)
	if err != nil {
		return SwitchResult{}, err
	}
	return s.switchTo(ctx, albumID, ed, false, why)
}

func (s *Service) switchTo(ctx context.Context, albumID int64, ed Edition, pinned bool, why string) (SwitchResult, error) {
	al, err := s.repo.GetAlbum(ctx, albumID)
	if err != nil {
		return SwitchResult{}, err
	}
	res, err := s.repo.SwitchEdition(ctx, albumID, ed.ID, pinned)
	if err != nil {
		return res, err
	}
	if res.From != res.To {
		n := len(audioTracks(ed))
		s.repo.AddEvent(ctx, al.ArtistID, "edition", fmt.Sprintf("Switched %q to the %d-track %s edition %s (%s)",
			al.Title, n, strings.TrimSpace(strings.Join(nonEmptyStrings(yearWord(ed.ReleaseDate), ed.Country, mediaWords(ed)), " ")),
			why, movedWords(res)))
	}
	return res, nil
}
