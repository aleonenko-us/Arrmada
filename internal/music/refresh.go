package music

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// Keeping an artist current (MUS-13) under the release-type rules (MUS-14).
//
// The one rule above all: nothing is wanted by surprise. An album a refresh adds is wanted
// only when it is a NEW release (MusicBrainz didn't list it for the artist before), it is
// not old music MusicBrainz has only now catalogued, the artist is monitored and its
// "new releases" choice is "all". Everything else a refresh adds is listed and not wanted,
// with the reason kept (found_as) so the New releases list can say why:
//
//   - not_new: MusicBrainz listed it before and it only passes now, because the artist's
//     release types changed. Changing types never wants anything by itself; the artist page
//     offers "Want matching albums", which shows the count first.
//   - back_catalogue: first released more than a year ago (or, on the first look at a
//     browse, more than 90 days ago or on an unknown date).
//   - too_many: more than maxNewPerRefresh new releases for one artist at once is a
//     catalogue change (an import, a merge), not a release week.
//   - single_pending → single_on_album: a single waits for its listing and is wanted only
//     when its songs aren't already on one of the artist's monitored albums.
//
// And the cache rule: MusicBrainz down, slow or answering with nothing changes nothing.
// An album that leaves the artist's list is re-pointed when MusicBrainz merged it, and
// flagged "no longer on MusicBrainz" when it's gone; it is never deleted or unmonitored.

// Why a refresh added an album wanted or not (albums.found_as).
const (
	FoundNew           = "new"
	FoundNotNew        = "not_new"
	FoundBackCatalogue = "back_catalogue"
	FoundTooMany       = "too_many"
	FoundSinglePending = "single_pending"
	FoundSingleOnAlbum = "single_on_album"
)

const (
	// maxNewPerRefresh: more new releases than this for one artist in one refresh are
	// listed, not wanted (see too_many). The books module uses the same guard.
	maxNewPerRefresh = 10
	// backCatalogueAge: a release group first out longer ago than this is old music
	// MusicBrainz has only now catalogued, not a new release.
	backCatalogueAge = 365 * 24 * time.Hour
	// firstLookWindow: on the first look at a browse (an upgraded library's first refresh,
	// or singles just switched on) only this recent counts as new.
	firstLookWindow = 90 * 24 * time.Hour
	// rgLookupsPerRefresh bounds the lookups that tell a merged album from a removed one,
	// per artist per refresh (each is one MusicBrainz call, cached).
	rgLookupsPerRefresh = 5
)

// browse scopes (artists.rg_seen_scopes)
const (
	scopeMain  = "main"  // albums and EPs
	scopeOther = "other" // singles, broadcasts, other
)

func scopeOf(primary string) string {
	switch strings.ToLower(strings.TrimSpace(primary)) {
	case "single", "broadcast", "other":
		return scopeOther
	}
	return scopeMain
}

// MonitorNewDefault is the library default for new releases ("all" unless saved).
func (s *Service) MonitorNewDefault() string {
	if v, ok := s.setting(KeyMonitorNew); ok && (v == MonitorNewAll || v == MonitorNewNone) {
		return v
	}
	return MonitorNewAll
}

// ReleaseTypesDefault is the library default release types.
func (s *Service) ReleaseTypesDefault() ReleaseTypes {
	if v, ok := s.setting(KeyReleaseTypes); ok {
		if t, err := ParseReleaseTypes(v); err == nil {
			return t
		}
	}
	return DefaultReleaseTypes()
}

// MonitorNewFor is the new-release choice in effect for an artist.
func (s *Service) MonitorNewFor(a Artist) string {
	if a.MonitorNew == MonitorNewAll || a.MonitorNew == MonitorNewNone {
		return a.MonitorNew
	}
	return s.MonitorNewDefault()
}

// ReleaseTypesFor is the release types in effect for an artist.
func (s *Service) ReleaseTypesFor(a Artist) ReleaseTypes {
	if a.ReleaseTypes != nil {
		return *a.ReleaseTypes
	}
	return s.ReleaseTypesDefault()
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// resultTypes is a MusicBrainz release group's primary and secondary types, reading the
// display label when a provider only gives that.
func resultTypes(r metadata.AlbumResult) (string, []string) {
	if r.PrimaryType == "" && len(r.SecondaryTypes) == 0 {
		p, sec := splitAlbumType(r.Type)
		return p, splitTypes(sec)
	}
	return r.PrimaryType, r.SecondaryTypes
}

// RefreshResult is what one artist refresh did.
type RefreshResult struct {
	ArtistID int64
	// Listed is how many of the artist's release groups its types list.
	Listed int
	// Added are albums added this time, each with FoundAs; Wanted are those of them now
	// monitored (new releases).
	Added  []Album
	Wanted []Album
	// Stale are albums MusicBrainz no longer lists (flagged, kept); Merged says what was
	// re-pointed.
	Stale  []Album
	Merged []string
	// Unanswered: MusicBrainz didn't answer (or answered with nothing where the library
	// has albums), so nothing changed.
	Unanswered bool
	// Calls is how many MusicBrainz requests went over the network.
	Calls int64
}

// syncMode says what a sync is for: adding the artist (the add dialog's choice applies to
// everything listed) or refreshing it (the new-release rules apply).
type syncMode int

const (
	syncAdd syncMode = iota
	syncRefresh
)

// pendingAlbum is one release group about to be added.
type pendingAlbum struct {
	r         metadata.AlbumResult
	primary   string
	secondary []string
	foundAs   string
	monitored bool
}

// syncAlbums reads an artist's release groups and brings its albums up to date: release
// groups its types list are added (or their details corrected), albums MusicBrainz merged
// are re-pointed, and albums it no longer lists are flagged. monitor is the add dialog's
// choice (syncAdd only).
func (s *Service) syncAlbums(ctx context.Context, a Artist, mode syncMode, monitor string) (RefreshResult, error) {
	res := RefreshResult{ArtistID: a.ID}
	ctx, tr := metadata.WithMBTrace(ctx)
	defer func() { res.Calls = tr.Fetched.Load() }()
	types := s.ReleaseTypesFor(a)
	now := s.clock()

	main, err := s.meta.ArtistAlbums(ctx, a.MBID)
	if err != nil {
		res.Unanswered = true
		return res, err
	}
	mainFresh := tr.Unanswered() == 0
	if mode == syncRefresh && !mainFresh {
		// MusicBrainz couldn't be asked and the cache answered with what it had: that is
		// exactly what the library already holds, so there is nothing to do but say so.
		res.Unanswered = true
		return res, nil
	}
	answer := append([]metadata.AlbumResult(nil), main...)
	scopes := []string{scopeMain}
	otherFresh := false
	if types.WantsOther() {
		if op, ok := s.meta.(metadata.OtherReleaseGroupsProvider); ok {
			before := tr.Unanswered()
			other, oerr := op.ArtistOtherReleaseGroups(ctx, a.MBID)
			if oerr != nil {
				// Albums and EPs still go ahead; singles wait for the next refresh.
				s.log.Warn("music: couldn't list the artist's singles", "artist", a.Name, "err", oerr)
			} else {
				answer = append(answer, other...)
				scopes = append(scopes, scopeOther)
				otherFresh = tr.Unanswered() == before
			}
		}
	}

	existing, err := s.repo.AlbumsFor(ctx, a.ID)
	if err != nil {
		return res, err
	}
	if len(main) == 0 && len(existing) > 0 {
		// Nothing listed where the library has albums: MusicBrainz having a bad moment,
		// never "the artist has no albums". Nothing changes.
		res.Unanswered = true
		return res, nil
	}

	inAnswer := map[string]bool{}
	for _, r := range answer {
		inAnswer[r.MBID] = true
	}

	// Albums that left the list. Only a list MusicBrainz answered just now says anything:
	// a stale copy served while it was down is no evidence of anything.
	deferNew := false
	if mode == syncRefresh {
		fresh := map[string]bool{scopeMain: mainFresh, scopeOther: otherFresh}
		var gone []Album
		for _, al := range existing {
			if al.MBID == "" || inAnswer[al.MBID] || al.StaleSince != "" || (al.Source != "" && al.Source != "musicbrainz") {
				continue
			}
			p, _ := albumTypes(al)
			if !fresh[scopeOf(p)] {
				continue
			}
			gone = append(gone, al)
		}
		unresolved := s.resolveGone(ctx, a, gone, inAnswer, now, &res)
		// A merged album may be among the "new" ones; until every departure is explained,
		// nothing new is added (the next refresh carries on where this one stopped).
		deferNew = unresolved > 0
	}

	seen, err := s.repo.SeenReleaseGroups(ctx, a.ID)
	if err != nil {
		return res, err
	}
	var pending []pendingAlbum
	newCount := 0
	for _, r := range answer {
		primary, secondary := resultTypes(r)
		if !types.Allows(primary, secondary) {
			continue
		}
		res.Listed++
		if _, ok, err := s.repo.AlbumIDByMBID(ctx, r.MBID); err != nil {
			return res, err
		} else if ok {
			// Already in the library: correct its details, never its monitoring.
			if _, err := s.repo.UpsertAlbum(ctx, albumFromResult(a.ID, r, primary, secondary)); err != nil {
				s.log.Warn("music: storing an album failed", "album", r.Title, "err", err)
			}
			continue
		}
		if deferNew {
			continue
		}
		p := pendingAlbum{r: r, primary: primary, secondary: secondary}
		if mode == syncRefresh {
			scope := scopeOf(primary)
			switch {
			case seen[r.MBID]:
				p.foundAs = FoundNotNew
			case !hasScope(a.seenScopes, scope):
				if recentOrUpcoming(r.ReleaseDate, now, firstLookWindow) {
					p.foundAs = FoundNew
				} else {
					p.foundAs = FoundBackCatalogue
				}
			case olderThan(r.ReleaseDate, now, backCatalogueAge):
				p.foundAs = FoundBackCatalogue
			default:
				p.foundAs = FoundNew
			}
			if p.foundAs == FoundNew {
				newCount++
			}
		}
		pending = append(pending, p)
	}

	// Who is wanted.
	wantNew := a.Monitored && s.MonitorNewFor(a) == MonitorNewAll
	latest := ""
	if mode == syncAdd && monitor == MonitorLatest {
		latest = pickLatest(pending, now)
	}
	for i := range pending {
		p := &pending[i]
		if mode == syncAdd {
			switch monitor {
			case MonitorAll:
				p.monitored = a.Monitored
			case MonitorLatest:
				p.monitored = a.Monitored && p.r.MBID == latest
			}
		} else if p.foundAs == FoundNew {
			if newCount > maxNewPerRefresh {
				p.foundAs = FoundTooMany
			} else {
				p.monitored = wantNew
			}
		}
		if p.monitored && strings.EqualFold(p.primary, "Single") {
			// A single is wanted only once it's known not to be on an album already
			// (checked by the refresh task when its listing is in).
			p.monitored, p.foundAs = false, FoundSinglePending
		}
	}
	if newCount > maxNewPerRefresh {
		s.log.Warn("music: MusicBrainz shows many new releases at once for one artist; listing them, not wanting them",
			"artist", a.Name, "new", newCount)
	}

	for _, p := range pending {
		al := albumFromResult(a.ID, p.r, p.primary, p.secondary)
		al.Monitored, al.FoundAs = p.monitored, p.foundAs
		if mode == syncRefresh {
			al.FoundAt = dbTime(now)
		}
		id, err := s.repo.UpsertAlbum(ctx, al)
		if err != nil {
			s.log.Warn("music: storing an album failed", "album", p.r.Title, "err", err)
			continue
		}
		al.ID = id
		res.Added = append(res.Added, al)
		if al.Monitored {
			res.Wanted = append(res.Wanted, al)
		}
	}

	// Everything the browses showed is seen from now on, listed or not, so a later change
	// of types can tell old music from new.
	all := make([]string, 0, len(answer))
	for _, r := range answer {
		all = append(all, r.MBID)
	}
	if !deferNew {
		if err := s.repo.MarkSeen(ctx, a.ID, all, scopes); err != nil {
			return res, err
		}
	}
	return res, nil
}

// albumFromResult is a release group as an album row.
func albumFromResult(artistID int64, r metadata.AlbumResult, primary string, secondary []string) Album {
	return Album{
		ArtistID: artistID, MBID: r.MBID, Title: r.Title, Year: r.Year, AlbumType: r.Type,
		CoverURL: r.CoverURL, ReleaseDate: r.ReleaseDate,
		PrimaryType: primary, SecondaryTypes: strings.Join(secondary, ","),
	}
}

// recentOrUpcoming reports whether a release date is unknown-free and within window of now
// (or in the future).
func recentOrUpcoming(date string, now time.Time, window time.Duration) bool {
	d := releaseDay(date)
	return !d.IsZero() && !d.Before(now.Add(-window))
}

// olderThan reports whether a release date is known and further back than age.
func olderThan(date string, now time.Time, age time.Duration) bool {
	d := releaseDay(date)
	return !d.IsZero() && d.Before(now.Add(-age))
}

// pickLatest is the "latest" choice: the newest album that is certainly out (an announced
// one never counts), an Album over an EP or anything else when there is one.
func pickLatest(pending []pendingAlbum, now time.Time) string {
	best, bestDay, bestAlbum := "", time.Time{}, false
	for _, p := range pending {
		if !releasedBy(p.r.ReleaseDate, now) {
			continue
		}
		day := releaseDay(p.r.ReleaseDate)
		isAlbum := strings.EqualFold(p.primary, "Album") || p.primary == ""
		switch {
		case best == "":
		case isAlbum && !bestAlbum:
		case !isAlbum && bestAlbum:
			continue
		case !day.After(bestDay):
			continue
		}
		best, bestDay, bestAlbum = p.r.MBID, day, isAlbum
	}
	return best
}

// resolveGone explains the albums that left an artist's list, a few lookups per refresh:
// merged into another release group → re-pointed (or, when the library already has that
// one too, flagged and both kept); removed → flagged "no longer on MusicBrainz". An album
// MusicBrainz still knows (now credited elsewhere) is left as it is, and one it couldn't
// answer about waits. It returns how many it couldn't explain this time.
func (s *Service) resolveGone(ctx context.Context, a Artist, gone []Album, inAnswer map[string]bool, now time.Time, res *RefreshResult) int {
	if len(gone) == 0 {
		return 0
	}
	rp, ok := s.meta.(metadata.ReleaseGroupProvider)
	unresolved := 0
	for i, al := range gone {
		if !ok {
			// No way to ask about one group: a successful list without it is the answer.
			s.flagGone(ctx, a, al, now, res)
			continue
		}
		if i >= rgLookupsPerRefresh {
			unresolved++
			continue
		}
		rg, err := rp.ReleaseGroup(ctx, al.MBID)
		switch {
		case metadata.IsNotFound(err):
			s.flagGone(ctx, a, al, now, res)
		case err != nil:
			unresolved++
		case rg.MBID != "" && rg.MBID != al.MBID:
			s.followAlbumMerge(ctx, a, al, rg, inAnswer, now, res)
		default:
			// Still on MusicBrainz, just not under this artist any more (its credit
			// changed). It stays in the library as it is.
			s.log.Debug("music: an album left the artist's MusicBrainz list but still exists; keeping it", "artist", a.Name, "album", al.Title)
		}
	}
	return unresolved
}

func (s *Service) flagGone(ctx context.Context, a Artist, al Album, now time.Time, res *RefreshResult) {
	if err := s.repo.SetAlbumStale(ctx, al.ID, now); err != nil {
		s.log.Warn("music: flagging an album failed", "album", al.Title, "err", err)
		return
	}
	al.StaleSince = dbTime(now)
	res.Stale = append(res.Stale, al)
	s.repo.AddEvent(ctx, a.ID, "refreshed", fmt.Sprintf("%q is no longer on MusicBrainz; kept, with its files", al.Title))
}

func (s *Service) followAlbumMerge(ctx context.Context, a Artist, al Album, rg metadata.AlbumResult, inAnswer map[string]bool, now time.Time, res *RefreshResult) {
	err := s.repo.RepointAlbum(ctx, al.ID, al.MBID, rg.MBID)
	switch {
	case errors.Is(err, ErrMBIDTaken):
		// Both halves of the merge are in the library: neither is folded into the other
		// by itself (that would move files between albums). The old one is flagged.
		if serr := s.repo.SetAlbumStale(ctx, al.ID, now); serr == nil {
			res.Stale = append(res.Stale, al)
		}
		s.repo.AddEvent(ctx, a.ID, "refreshed", fmt.Sprintf("MusicBrainz merged %q into %q, which the library also has; both kept", al.Title, rg.Title))
		_ = s.repo.AddExternalID(ctx, ExternalID{EntityType: "album", EntityID: al.ID, Source: "musicbrainz_merged_into", Value: rg.MBID})
	case err != nil:
		s.log.Warn("music: following a merged album failed", "album", al.Title, "err", err)
	default:
		inAnswer[rg.MBID] = true
		res.Merged = append(res.Merged, fmt.Sprintf("%s → %s", al.MBID, rg.MBID))
		s.repo.AddEvent(ctx, a.ID, "refreshed", fmt.Sprintf("MusicBrainz merged %q; following its new entry", al.Title))
		s.log.Info("music: followed a merged album", "album", al.Title, "from", al.MBID, "to", rg.MBID)
	}
}

// followArtistMerge re-points an artist MusicBrainz answered under a new MBID.
func (s *Service) followArtistMerge(ctx context.Context, a *Artist, newMBID string) {
	err := s.repo.RepointArtist(ctx, a.ID, a.MBID, newMBID)
	switch {
	case errors.Is(err, ErrMBIDTaken):
		_ = s.repo.AddExternalID(ctx, ExternalID{EntityType: "artist", EntityID: a.ID, Source: "musicbrainz_merged_into", Value: newMBID})
		s.repo.AddEvent(ctx, a.ID, "refreshed", "MusicBrainz merged this artist into another one the library already has; both kept")
	case err != nil:
		s.log.Warn("music: following a merged artist failed", "artist", a.Name, "err", err)
	default:
		s.log.Info("music: followed a merged artist", "artist", a.Name, "from", a.MBID, "to", newMBID)
		s.repo.AddEvent(ctx, a.ID, "refreshed", "MusicBrainz merged this artist's entry; following its new id")
		a.MBID = newMBID
	}
}

// RefreshOptions say how a refresh was asked for.
type RefreshOptions struct {
	// Scheduled refreshes write history only when something changed.
	Scheduled bool
}

// RefreshArtist re-reads an artist and its albums from MusicBrainz (stale cached answers
// asked again; fresh ones free) under the rules at the top of this file.
func (s *Service) RefreshArtist(ctx context.Context, id int64, opt RefreshOptions) (RefreshResult, error) {
	a, err := s.repo.GetArtist(ctx, id)
	if err != nil {
		return RefreshResult{ArtistID: id}, err
	}
	ctx, tr := metadata.WithMBTrace(metadata.WithMBRevalidate(ctx))
	now := s.clock()
	if d, derr := s.meta.GetArtist(ctx, a.MBID); derr == nil {
		if d.MBID != "" && d.MBID != a.MBID {
			s.followArtistMerge(ctx, &a, d.MBID)
		}
		if tr.Unanswered() == 0 {
			if err := s.repo.UpdateArtistMeta(ctx, id, d.Overview, a.ImageURL, d.Genres, d.Disambiguation, d.Type, d.Country); err != nil {
				s.log.Warn("music: updating artist metadata failed", "artist", a.Name, "err", err)
			}
		}
	} else {
		s.log.Warn("music: artist metadata refresh failed", "artist", a.Name, "err", derr)
	}
	res, serr := s.syncAlbums(ctx, a, syncRefresh, "")
	res.Calls = tr.Fetched.Load()
	if serr != nil && !res.Unanswered {
		// A database failure: say so, and leave the artist due.
		return res, serr
	}
	if res.Unanswered {
		s.log.Warn("music: MusicBrainz didn't answer for the artist; kept what we had", "artist", a.Name, "err", serr)
		_ = s.repo.SetArtistStale(ctx, id, now)
		s.repo.AddEvent(ctx, id, "refreshed", "MusicBrainz didn't answer; kept what we had")
	} else {
		s.writeRefreshEvents(ctx, a, res, opt)
	}
	if err := s.repo.StampRefreshed(ctx, id, now); err != nil {
		return res, err
	}
	return res, nil
}

// writeRefreshEvents records what a refresh found in the artist's history: each new
// release that is wanted, one line for everything listed but not wanted, and (for a
// refresh someone asked for) a summary.
func (s *Service) writeRefreshEvents(ctx context.Context, a Artist, res RefreshResult, opt RefreshOptions) {
	var listed []string
	for _, al := range res.Added {
		if al.Monitored {
			s.repo.AddEvent(ctx, a.ID, "new", "New release: "+releaseWords(al))
			continue
		}
		listed = append(listed, al.Title)
	}
	if len(listed) > 0 {
		s.repo.AddEvent(ctx, a.ID, "refreshed", fmt.Sprintf("Listed %d more from MusicBrainz, not wanted: %s", len(listed), wordList(listed, 5)))
	}
	if !opt.Scheduled {
		s.repo.AddEvent(ctx, a.ID, "refreshed", fmt.Sprintf("Refreshed — %d album(s) listed", res.Listed))
	}
}

// releaseWords is "Title (EP, 2026-10-03)".
func releaseWords(al Album) string {
	var bits []string
	if al.AlbumType != "" {
		bits = append(bits, al.AlbumType)
	}
	if al.ReleaseDate != "" {
		bits = append(bits, al.ReleaseDate)
	}
	if len(bits) == 0 {
		return al.Title
	}
	return fmt.Sprintf("%s (%s)", al.Title, strings.Join(bits, ", "))
}

// wordList is up to n names and "and N more".
func wordList(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:n], ", "), len(names)-n)
}

// CheckPendingSingles settles singles waiting for the singles-on-an-album check, up to
// limit: each single's listing and the listings of the artist's monitored albums are read
// (through the cache), and the single is wanted only when some song on it isn't on one of
// those albums. more() is asked before each MusicBrainz-bound step; when it says no, the
// rest wait for the next run. It returns how many were settled and how many wanted.
func (s *Service) CheckPendingSingles(ctx context.Context, limit int, more func() bool) (settled, wanted int, err error) {
	singles, err := s.repo.PendingSingles(ctx, s.clock().Add(-24*time.Hour), limit)
	if err != nil {
		return 0, 0, err
	}
	for _, single := range singles {
		if ctx.Err() != nil || !more() {
			return settled, wanted, ctx.Err()
		}
		a, err := s.repo.GetArtist(ctx, single.ArtistID)
		if err != nil {
			continue
		}
		on, known, err := s.singleOnAlbum(ctx, a, single, more)
		if err != nil {
			return settled, wanted, err
		}
		if !known {
			// Its listing (or an album's) isn't in yet. When the budget ran out that's
			// the next run's job; otherwise MusicBrainz has nothing yet, so a day's wait.
			if more() {
				if err := s.repo.StampListingTried(ctx, single.ID, s.clock()); err != nil {
					return settled, wanted, err
				}
			}
			continue
		}
		settled++
		if on {
			if err := s.repo.SetFound(ctx, single.ID, FoundSingleOnAlbum, false); err != nil {
				return settled, wanted, err
			}
			s.repo.AddEvent(ctx, a.ID, "refreshed", fmt.Sprintf("Single %q is already on an album; listed, not wanted", single.Title))
			continue
		}
		as := ""
		if single.FoundAt != "" {
			as = FoundNew
		}
		want := a.Monitored
		if err := s.repo.SetFound(ctx, single.ID, as, want); err != nil {
			return settled, wanted, err
		}
		if want {
			wanted++
			s.repo.AddEvent(ctx, a.ID, "new", "New release: "+releaseWords(single))
		}
	}
	return settled, wanted, nil
}

// singleOnAlbum reports whether every song on a single is already on one of the artist's
// monitored albums (by recording MBID, or by title without its version suffix, so the
// radio edit counts as the album track). known is false while a listing it needs isn't
// in.
func (s *Service) singleOnAlbum(ctx context.Context, a Artist, single Album, more func() bool) (on, known bool, err error) {
	if single.EditionTracks == 0 {
		if err := s.EnsureTracks(ctx, single); err != nil {
			return false, false, nil
		}
	}
	tracks, err := s.repo.TracksFor(ctx, single.ID)
	if err != nil {
		return false, false, err
	}
	if len(tracks) == 0 {
		return false, false, nil // no listing yet (an announced single)
	}
	albums, err := s.repo.AlbumsFor(ctx, a.ID)
	if err != nil {
		return false, false, err
	}
	var ids []int64
	for _, al := range albums {
		p, _ := albumTypes(al)
		if al.ID == single.ID || !al.Monitored || strings.EqualFold(p, "Single") {
			continue
		}
		if al.EditionTracks == 0 {
			if !more() {
				return false, false, nil
			}
			if err := s.EnsureTracks(ctx, al); err != nil {
				return false, false, nil
			}
		}
		ids = append(ids, al.ID)
	}
	songs, err := s.repo.songsOf(ctx, ids)
	if err != nil {
		return false, false, err
	}
	for _, t := range tracks {
		if t.RecordingMBID != "" && songs.recordings[strings.ToLower(t.RecordingMBID)] {
			continue
		}
		if title := songTitle(t.Title); title != "" && songs.titles[title] {
			continue
		}
		return false, true, nil
	}
	return true, true, nil
}

// ArtistOptions is an artist's new-release and release-type choices as the options panel
// shows them: its own (empty / nil = the library default), what's in effect, and the
// defaults.
type ArtistOptions struct {
	MonitorNew            string        `json:"monitor_new"`
	ReleaseTypes          *ReleaseTypes `json:"release_types"`
	EffectiveMonitorNew   string        `json:"effective_monitor_new"`
	EffectiveReleaseTypes ReleaseTypes  `json:"effective_release_types"`
	DefaultMonitorNew     string        `json:"default_monitor_new"`
	DefaultReleaseTypes   ReleaseTypes  `json:"default_release_types"`
	QualityProfile        string        `json:"quality_profile"`
	// Apply is what "Want matching albums" would do now.
	Apply ApplyPlan `json:"apply"`
}

// Options reads an artist's options.
func (s *Service) Options(ctx context.Context, id int64) (ArtistOptions, error) {
	a, err := s.repo.GetArtist(ctx, id)
	if err != nil {
		return ArtistOptions{}, err
	}
	plan, err := s.ApplyPreview(ctx, id)
	if err != nil {
		return ArtistOptions{}, err
	}
	return ArtistOptions{
		MonitorNew: a.MonitorNew, ReleaseTypes: a.ReleaseTypes,
		EffectiveMonitorNew: s.MonitorNewFor(a), EffectiveReleaseTypes: s.ReleaseTypesFor(a),
		DefaultMonitorNew: s.MonitorNewDefault(), DefaultReleaseTypes: s.ReleaseTypesDefault(),
		QualityProfile: a.QualityProfile, Apply: plan,
	}, nil
}

// SetOptions stores an artist's own choices (monitorNew "" and types nil: follow the
// library default). It changes what new albums get and which release groups are listed;
// it never wants or unwants an album by itself. It reports whether the types in effect
// changed (the caller then refreshes the artist, so newly listed albums appear).
func (s *Service) SetOptions(ctx context.Context, id int64, monitorNew string, types *ReleaseTypes) (bool, error) {
	if !ValidMonitorNew(monitorNew) {
		return false, fmt.Errorf("unknown new-release choice %q", monitorNew)
	}
	a, err := s.repo.GetArtist(ctx, id)
	if err != nil {
		return false, err
	}
	stored := ""
	if types != nil {
		t, err := types.Normalize()
		if err != nil {
			return false, err
		}
		types, stored = &t, t.String()
	}
	before := s.ReleaseTypesFor(a)
	if err := s.repo.SetArtistOptions(ctx, id, monitorNew, stored); err != nil {
		return false, err
	}
	a.MonitorNew, a.ReleaseTypes = monitorNew, types
	return !before.Equal(s.ReleaseTypesFor(a)), nil
}

// AlbumBrief names an album in a plan.
type AlbumBrief struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type,omitempty"`
	Year  int    `json:"year,omitempty"`
}

// ApplyPlan is what applying an artist's release types to the albums it already lists
// would do: want the unwanted albums the types allow, stop wanting the wanted ones they
// no longer allow that hold no files, and put the singles they allow through the
// singles-on-an-album check. Nothing happens until a person confirms it.
type ApplyPlan struct {
	Want    []AlbumBrief `json:"want"`
	Unwant  []AlbumBrief `json:"unwant"`
	Singles []AlbumBrief `json:"singles"`
}

// Empty reports whether the plan would change nothing.
func (p ApplyPlan) Empty() bool { return len(p.Want)+len(p.Unwant)+len(p.Singles) == 0 }

// ApplyPreview works out an artist's apply plan.
func (s *Service) ApplyPreview(ctx context.Context, id int64) (ApplyPlan, error) {
	plan := ApplyPlan{Want: []AlbumBrief{}, Unwant: []AlbumBrief{}, Singles: []AlbumBrief{}}
	a, err := s.repo.GetArtist(ctx, id)
	if err != nil {
		return plan, err
	}
	albums, err := s.repo.AlbumsFor(ctx, id)
	if err != nil {
		return plan, err
	}
	types := s.ReleaseTypesFor(a)
	for _, al := range albums {
		p, sec := albumTypes(al)
		brief := AlbumBrief{ID: al.ID, Title: al.Title, Type: al.AlbumType, Year: al.Year}
		allowed := types.Allows(p, sec)
		switch {
		case allowed && !al.Monitored && al.StaleSince == "" && al.FoundAs != FoundSingleOnAlbum && al.FoundAs != FoundSinglePending:
			if strings.EqualFold(p, "Single") {
				plan.Singles = append(plan.Singles, brief)
			} else {
				plan.Want = append(plan.Want, brief)
			}
		case !allowed && al.Monitored && al.HaveTracks == 0 && al.SizeBytes == 0:
			plan.Unwant = append(plan.Unwant, brief)
		}
	}
	return plan, nil
}

// ErrPlanChanged is an apply refused because the plan isn't the one the person saw.
var ErrPlanChanged = errors.New("the albums changed since the preview; look again")

// ApplyOptions carries out an artist's apply plan, provided it is still the plan the
// person was shown (want/unwant/singles counts); otherwise ErrPlanChanged.
func (s *Service) ApplyOptions(ctx context.Context, id int64, want, unwant, singles int) (ApplyPlan, error) {
	plan, err := s.ApplyPreview(ctx, id)
	if err != nil {
		return plan, err
	}
	if len(plan.Want) != want || len(plan.Unwant) != unwant || len(plan.Singles) != singles {
		return plan, ErrPlanChanged
	}
	for _, b := range plan.Want {
		if err := s.repo.SetAlbumMonitored(ctx, b.ID, true); err != nil {
			return plan, err
		}
	}
	for _, b := range plan.Unwant {
		if err := s.repo.SetAlbumMonitored(ctx, b.ID, false); err != nil {
			return plan, err
		}
	}
	for _, b := range plan.Singles {
		if err := s.repo.SetFound(ctx, b.ID, FoundSinglePending, false); err != nil {
			return plan, err
		}
	}
	if !plan.Empty() {
		s.repo.AddEvent(ctx, id, "refreshed", fmt.Sprintf("Applied the release types: %d wanted, %d no longer wanted, %d singles to check",
			len(plan.Want), len(plan.Unwant), len(plan.Singles)))
	}
	return plan, nil
}

// NewReleases lists the albums refreshes found in the last days.
func (s *Service) NewReleases(ctx context.Context, days int) ([]NewRelease, error) {
	if days <= 0 {
		days = 30
	}
	return s.repo.NewReleases(ctx, s.clock().Add(-time.Duration(days)*24*time.Hour), 500)
}

// ArtistsDueForRefresh lists artists not refreshed within fresh, never-refreshed first.
func (s *Service) ArtistsDueForRefresh(ctx context.Context, fresh time.Duration, limit int) ([]Artist, error) {
	return s.repo.ArtistsDueForRefresh(ctx, s.clock().Add(-fresh), limit)
}

// AlbumsWithoutListing lists monitored albums with no listing yet, not tried within the
// last day.
func (s *Service) AlbumsWithoutListing(ctx context.Context, limit int) ([]Album, error) {
	return s.repo.AlbumsWithoutListing(ctx, s.clock().Add(-24*time.Hour), limit)
}

// FillListing fetches a monitored album's missing listing for the refresh (EnsureTracks),
// and notes the try either way, so an album MusicBrainz has nothing for yet waits a day
// rather than taking a slot every run.
func (s *Service) FillListing(ctx context.Context, al Album) error {
	err := s.EnsureTracks(ctx, al)
	if serr := s.repo.StampListingTried(ctx, al.ID, s.clock()); serr != nil && err == nil {
		err = serr
	}
	return err
}

// LegacyAlbums lists albums still on the upgrade's legacy edition whose editions weren't
// listed within editionsRecheck.
func (s *Service) LegacyAlbums(ctx context.Context, limit int) ([]Album, error) {
	return s.repo.LegacyAlbums(ctx, s.clock().Add(-editionsRecheck), limit)
}

// RekeyLegacyEditions is the background edition pass: up to limit albums still on the
// upgrade's legacy edition have their editions listed and are re-keyed to a real
// MusicBrainz edition, a few per run, so an upgraded library reaches real editions without
// anyone opening each album. It is EnsureEditions, so RekeyEdition's rules hold: a re-key
// happens only when every file finds its track and the album reads no less complete, with
// no track newly wanted; an album that fits nothing keeps its listing and isn't tried
// again until its editions are next due. more() is asked before each album. It returns
// how many albums were tried and how many moved to a real edition.
func (s *Service) RekeyLegacyEditions(ctx context.Context, limit int, more func() bool) (tried, rekeyed int, err error) {
	albums, err := s.LegacyAlbums(ctx, limit)
	if err != nil {
		return 0, 0, err
	}
	for _, al := range albums {
		if ctx.Err() != nil || !more() {
			return tried, rekeyed, ctx.Err()
		}
		tried++
		if err := s.EnsureEditions(ctx, al); err != nil {
			s.log.Info("music: the edition pass couldn't list an album's editions; trying again later", "album", al.Title, "err", err)
			continue
		}
		st, err := s.repo.editionState(ctx, al.ID)
		if err != nil {
			return tried, rekeyed, err
		}
		if st.HasTarget && !st.TargetLegacy {
			rekeyed++
		}
	}
	return tried, rekeyed, nil
}
