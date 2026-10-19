package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/music"
)

// Interactive search for an album (MUS-17): every release with the reason the automatic
// search would or wouldn't take it (music_judge.go), and a person's own grab.

// RankAlbumReleases runs an interactive search for one album and returns every release
// with whether the automatic search would take it and, when not, why: the same steps the
// sweep judges by. Nothing is grabbed. The recommended release is the sweep's pick.
//
// It is the automatic search's one query, so what it shows is what the sweep sees.
func (c *Coordinator) RankAlbumReleases(ctx context.Context, albumID int64) (ReleaseList, error) {
	if c.music == nil {
		return ReleaseList{}, music.ErrNotFound
	}
	al, err := c.music.AlbumRecord(ctx, albumID)
	if err != nil {
		return ReleaseList{}, err
	}
	a, err := c.music.GetArtist(ctx, al.ArtistID)
	if err != nil {
		return ReleaseList{}, err
	}
	sp := c.musicProfile(ctx, a.QualityProfile)
	// The listing sizes the plausibility check; an album with none is still searchable by
	// hand (a person may know better), it just isn't size-checked.
	var listing music.SizeListing
	if err := c.music.EnsureTracks(ctx, al); err == nil {
		if tracks, err := c.music.Tracks(ctx, al.ID); err == nil {
			listing = music.ListingOf(tracks)
		}
	}
	mode, err := c.albumModeOf(ctx, al)
	if err != nil {
		return ReleaseList{}, err
	}
	ctx, notes := withSearchNotes(ctx)
	res, err := c.searchMusic(ctx, albumQuery(a, al))
	if err != nil {
		if allIndexersFailed(err) {
			return ReleaseList{Profile: a.QualityProfile}.withIssues(notes), nil
		}
		return ReleaseList{}, err
	}
	nets := c.networks(ctx)
	j := &albumJudge{artist: a.Name, album: al.Title, sp: sp, nets: nets, listing: listing, mode: mode}
	judged, err := c.judgeAlbum(ctx, al.ID, j, res.Releases)
	if err != nil {
		return ReleaseList{}, fmt.Errorf("couldn't read the album's blocklist or pending grabs: %w", err)
	}
	// Eligible first, best first by the sweep's own order; then the rest, the nearest
	// misses first.
	sort.SliceStable(judged, func(i, k int) bool {
		x, y := judged[i], judged[k]
		if (x.stage == stageEligible) != (y.stage == stageEligible) {
			return x.stage == stageEligible
		}
		if x.stage == stageEligible {
			return nets.beats(x.rel, x.score, y.rel, y.score)
		}
		return x.stage > y.stage
	})
	var eligible []indexer.Release
	for _, r := range judged {
		if r.stage == stageEligible {
			eligible = append(eligible, r.rel)
		}
	}
	best := pickBestAlbum(nets, sp, eligible)
	list := ReleaseList{Profile: a.QualityProfile, Releases: make([]RankedRelease, 0, len(judged))}
	for _, r := range judged {
		rr := withIndexerFacts(RankedRelease{
			Title: r.rel.Title, Indexer: r.rel.Indexer, DownloadURL: r.rel.DownloadURL, InfoHash: r.rel.InfoHash,
			InfoURL: safeInfoURL(r.rel.InfoURL, r.rel.DownloadURL), SizeGB: r.rel.SizeGB(), Seeders: r.rel.Seeders,
			Summary: albumSummary(r), Format: string(r.tier),
			Eligible: r.stage == stageEligible, RejectReason: r.reason,
			Blocklisted: r.code == DropBlocklisted && r.stage == stageBlocked,
		}, r.rel, nets)
		if best != nil && r.stage == stageEligible && r.rel.Title == best.Title && r.rel.Indexer == best.Indexer {
			rr.Recommended = true
			list.Why = albumWhy(r, mode)
		}
		list.Releases = append(list.Releases, rr)
	}
	return list.withIssues(notes), nil
}

// albumModeOf is how an album is searched as it stands: an upgrade once it's complete, a
// search for missing tracks (at no lower a tier than it holds) otherwise.
func (c *Coordinator) albumModeOf(ctx context.Context, al music.Album) (albumMode, error) {
	if al.HaveTracks == 0 {
		return albumMode{}, nil
	}
	held, ok, err := c.music.HeldTiers(ctx, al.ID)
	if err != nil {
		return albumMode{}, err
	}
	if !ok {
		held = music.QualityUnknown
	}
	return albumMode{upgrade: al.Complete(), held: held}, nil
}

// albumSummary is a release's headline in interactive search: its tier and what its name
// says about the edition ("FLAC · Deluxe · 2CD").
func albumSummary(r albumRelease) string {
	parts := []string{string(r.tier)}
	if r.tier == music.QualityUnknown {
		parts[0] = "Unknown quality"
	}
	parts = append(parts, music.EditionHints(r.rel.Title)...)
	return strings.Join(parts, " · ")
}

// albumWhy says why the recommended release won.
func albumWhy(r albumRelease, mode albumMode) []string {
	why := []string{fmt.Sprintf("%s, the best tier found that your profile takes", r.tier)}
	if mode.upgrade {
		why = append(why, fmt.Sprintf("Better than the %s you have", mode.held))
	}
	if r.rel.Seeders > 0 {
		why = append(why, fmt.Sprintf("%d seeders", r.rel.Seeders))
	}
	return why
}

// ErrGrabInFlight is a manual grab of a release already grabbed for the album and not
// imported or closed yet.
var ErrGrabInFlight = errors.New("that release is already downloading for this album")

// GrabForAlbum grabs a release a person chose in interactive search for one album. It is
// recorded against the album, so the import matches the download against it (the matcher
// still decides which file is which track and holds anything doubtful for Reviews), and
// marked as the person's choice.
func (c *Coordinator) GrabForAlbum(ctx context.Context, albumID int64, indexerName, downloadURL, title string) error {
	if c.music == nil {
		return music.ErrNotFound
	}
	if !c.moduleOn(ctx, "music") {
		return ErrModuleOff
	}
	al, err := c.music.AlbumRecord(ctx, albumID)
	if err != nil {
		return err
	}
	a, err := c.music.GetArtist(ctx, al.ArtistID)
	if err != nil {
		return err
	}
	// The same release already on its way for this album: a second copy would only race it.
	pending, err := c.pendingMusicGrabTitles(ctx, al.ID)
	if err != nil {
		return err
	}
	if pending[normTitle(title)] {
		return ErrGrabInFlight
	}
	hash, err := c.grabMusic(ctx, indexerName, downloadURL, title, musicCategory)
	if err != nil {
		return err
	}
	c.recordMusicGrab(ctx, "music", al.ID, title, indexerName, a.QualityProfile, hash)
	c.markGrabManual(ctx, hash)
	c.music.AddEvent(ctx, a.ID, "grabbed", fmt.Sprintf("Grabbed %q by hand from %s: %s", al.Title, indexerName, title))
	c.log.Info("music: grabbed by hand", "artist", a.Name, "album", al.Title, "release", title)
	return nil
}
