package automation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/music"
)

// MusicScanResult summarizes a library scan.
type MusicScanResult struct {
	Artists   int      `json:"artists"`   // artists newly catalogued
	Albums    int      `json:"albums"`    // albums matched to a library album
	Tracks    int      `json:"tracks"`    // track files recorded
	Skipped   int      `json:"skipped"`   // album folders already fully accounted for
	Unmatched []string `json:"unmatched"` // folders we refused to guess at
	Measured  int      `json:"measured"`  // tracks already on record measured for their real tier
}

// albumFolder is one on-disk folder holding an album's audio files.
type albumFolder struct {
	path   string
	artist string // from the parent directory, or the "Artist - Album" folder name
	album  string
	year   int
	files  []music.AudioFile
}

// ScanMusicLibrary catalogues music already on disk.
//
// Deliberately conservative about matching. The Books scan takes the FIRST search hit for
// whatever it parsed out of a folder name, which quietly files real audio under the wrong
// title and then makes that wrong entry a delete hazard. This refuses to guess: an artist
// is only created when a MusicBrainz search returns an exact name match, an album is only
// filled when its title matches one the artist actually has, and everything else is
// reported in Unmatched for a human to look at.
//
// Nothing is deleted or moved — this only records what's already there.
//
// progress, when set, hears how many album folders are done out of how many there are.
// A cancelled or timed-out scan stops between folders and returns an error saying how
// far it got: the folder it was on is finished (an artist it added gets the album's
// tracks recorded), nothing after it is touched, and folders cut short aren't reported
// as unrecognised.
func (c *Coordinator) ScanMusicLibrary(ctx context.Context, progress func(done, total int)) (MusicScanResult, error) {
	var res MusicScanResult
	res.Unmatched = []string{}
	if progress == nil {
		progress = func(int, int) {}
	}
	if c.music == nil || c.imp == nil {
		return res, nil
	}
	if !c.moduleOn(ctx, "music") {
		return res, ErrModuleOff
	}
	root := c.imp.MusicDir()
	folders := findAlbumFolders(root)
	if len(folders) == 0 {
		return res, nil
	}
	c.log.Info("music scan: walking the library", "root", root, "album_folders", len(folders))

	// Cache the library and each artist's albums across the whole scan: a big collection
	// would otherwise re-read the artist list once per folder.
	known, err := c.music.ListArtists(ctx)
	if err != nil {
		return res, err
	}
	byName := make(map[string]music.Artist, len(known))
	for _, a := range known {
		byName[music.NormKey(a.Name)] = a
	}

	for i, f := range folders {
		progress(i, len(folders))
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("the scan stopped after %d of %d album folders: %w", i, len(folders), err)
		}
		// A folder started is finished: the artist lookup, adding the artist and recording
		// the album's tracks run to the end even if the scan is stopped meanwhile.
		fctx := context.WithoutCancel(ctx)
		artist, ok := c.scanResolveArtist(fctx, f, byName, &res)
		if !ok {
			res.Unmatched = append(res.Unmatched, f.path)
			continue
		}
		albums, err := c.music.Albums(fctx, artist.ID)
		if err != nil {
			continue
		}
		album, ok := matchAlbumByTitle(albums, f.album)
		if !ok {
			// The artist is known but this folder isn't an album they have. Refusing here is
			// the point: filing it under a near-miss would put real audio on the wrong record.
			res.Unmatched = append(res.Unmatched, f.path)
			continue
		}
		n, err := c.recordAlbumFiles(fctx, artist, album, f)
		if err != nil {
			c.log.Warn("music scan: recording an album failed", "album", album.Title, "err", err)
			continue
		}
		if n == 0 {
			res.Skipped++
			continue
		}
		res.Albums++
		res.Tracks += n
	}
	progress(len(folders), len(folders))
	res.Measured = c.measureUnmeasuredTracks(ctx, musicMeasurePerScan)
	c.log.Info("music scan: finished", "artists", res.Artists, "albums", res.Albums,
		"tracks", res.Tracks, "skipped", res.Skipped, "unmatched", len(res.Unmatched), "measured", res.Measured)
	return res, nil
}

// musicMeasurePerScan caps how many already-recorded tracks one scan measures, so a big
// library recorded before tracks were probed catches up over a few scans.
const musicMeasurePerScan = 200

// measureUnmeasuredTracks probes up to limit tracks that have a file but no measured
// bitrate (recorded before imports were probed, or by a scan that couldn't probe) and
// records their real tier and bitrate. ffprobe only reads; nothing is moved or rewritten.
// A file that's gone or unreadable is skipped and stays unmeasured; a cursor carries each
// pass on from where the last one stopped, so files like that never hold the rest back,
// and it starts over from the top once it reaches the end.
func (c *Coordinator) measureUnmeasuredTracks(ctx context.Context, limit int) int {
	if c.probeAudioFn == nil && !mediainfo.Available() {
		return 0
	}
	tracks, err := c.music.UnmeasuredTracks(ctx, c.measureCursor.Load(), limit)
	if err != nil {
		c.log.Warn("music scan: couldn't list unmeasured tracks", "err", err)
		return 0
	}
	if len(tracks) < limit {
		c.measureCursor.Store(0) // the end: the next pass starts from the top again
	}
	n := 0
	for _, t := range tracks {
		if ctx.Err() != nil {
			break
		}
		if len(tracks) == limit {
			c.measureCursor.Store(t.ID)
		}
		info, err := c.probeAudio(ctx, t.FilePath)
		if err != nil {
			continue
		}
		q := music.TierFromProbe(info)
		if q == music.QualityUnknown || info.BitrateKbps <= 0 {
			continue
		}
		if err := c.music.SetTrackMeasured(ctx, t.ID, t.FilePath, string(q), info.BitrateKbps); err != nil {
			c.log.Warn("music scan: recording a measured tier failed", "track", t.Title, "err", err)
			continue
		}
		n++
	}
	return n
}

// scanResolveArtist finds the library artist for a folder, adding one only on an exact
// MusicBrainz name match. Newly added artists are UNMONITORED: they already exist on disk,
// and switching every scanned artist on would send the searcher after their entire
// discography the moment the scan finished.
func (c *Coordinator) scanResolveArtist(ctx context.Context, f albumFolder, byName map[string]music.Artist, res *MusicScanResult) (music.Artist, bool) {
	key := music.NormKey(f.artist)
	if key == "" {
		return music.Artist{}, false
	}
	if a, ok := byName[key]; ok {
		return a, true
	}
	results, err := c.music.Lookup(ctx, f.artist)
	if err != nil || len(results) == 0 {
		return music.Artist{}, false
	}
	match := ""
	for _, r := range results {
		if music.NormKey(r.Name) == key {
			if match != "" && match != r.MBID {
				// Two artists share this exact name (MusicBrainz has several "Nirvana").
				// There's no basis to choose, so don't.
				return music.Artist{}, false
			}
			match = r.MBID
		}
	}
	if match == "" {
		return music.Artist{}, false
	}
	added, err := c.music.AddArtist(ctx, match, "", false)
	if err != nil {
		c.log.Warn("music scan: adding a scanned artist failed", "artist", f.artist, "err", err)
		return music.Artist{}, false
	}
	byName[key] = added
	res.Artists++
	c.log.Info("music scan: catalogued a new artist", "artist", added.Name, "from", f.path)
	return added, true
}

// recordAlbumFiles matches a folder's audio onto the album's tracks and records them.
func (c *Coordinator) recordAlbumFiles(ctx context.Context, a music.Artist, al music.Album, f albumFolder) (int, error) {
	if err := c.music.EnsureTracks(ctx, al); err != nil {
		return 0, err
	}
	tracks, err := c.music.Tracks(ctx, al.ID)
	if err != nil || len(tracks) == 0 {
		return 0, err
	}
	matched, unmatched := music.MatchTracks(tracks, f.files)
	if len(unmatched) > 0 {
		names := make([]string, 0, len(unmatched))
		for _, u := range unmatched {
			names = append(names, filepath.Base(u.Path))
		}
		c.log.Info("music scan: some files matched no track", "album", al.Title, "files", strings.Join(names, ", "))
	}
	n := 0
	for _, t := range tracks {
		file, ok := matched[t.ID]
		if !ok || t.HasFile {
			continue // already recorded, or nothing on disk for it
		}
		// Measured when it can be (read-only ffprobe), else the extension's tag.
		tier, bitrate := c.trackTier(ctx, file.Path, music.QualityUnknown)
		// The file is already in the library — record where it is, don't move it.
		if err := c.music.MarkTrackImported(ctx, t.ID, file.Path, string(tier), bitrate, file.SizeBytes, ""); err != nil {
			c.log.Warn("music scan: recording a track failed", "track", t.Title, "err", err)
			continue
		}
		n++
	}
	if n > 0 {
		c.music.AddEvent(ctx, a.ID, "imported", "Found "+al.Title+" during a library scan")
	}
	return n, nil
}

// matchAlbumByTitle finds the artist's album whose title matches the folder, exact
// normalized comparison only.
func matchAlbumByTitle(albums []music.Album, title string) (music.Album, bool) {
	want := music.NormKey(title)
	if want == "" {
		return music.Album{}, false
	}
	for _, al := range albums {
		if music.NormKey(al.Title) == want {
			return al, true
		}
	}
	return music.Album{}, false
}

// findAlbumFolders walks a music library and returns every folder holding audio.
//
// The layout assumed is the conventional <Artist>/<Album>/tracks. A folder sitting directly
// under the root is read as "Artist - Album" instead, which is the other common shape.
func findAlbumFolders(root string) []albumFolder {
	byDir := map[string][]music.AudioFile{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		// The recycle bin (.arrmada-recycle) and other hidden folders aren't albums: a
		// deleted album scanned from the bin would come straight back.
		if err == nil && d.IsDir() && p != root && library.SkipScanDir(d.Name()) {
			return filepath.SkipDir
		}
		if err != nil || d.IsDir() || !music.IsAudioFile(p) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() < 256<<10 {
			return nil // artwork, a jingle or a broken file — not a track
		}
		dir := filepath.Dir(p)
		byDir[dir] = append(byDir[dir], music.AudioFile{Path: p, SizeBytes: info.Size()})
		return nil
	})

	out := make([]albumFolder, 0, len(byDir))
	for dir, files := range byDir {
		f := albumFolder{path: dir, files: files}
		base := filepath.Base(dir)
		parent := filepath.Dir(dir)
		if sameDir(parent, root) {
			// No artist level: read the folder itself as "Artist - Album".
			rel := music.ParseRelease(base)
			f.artist, f.album, f.year = rel.Artist, rel.Album, rel.Year
		} else {
			f.artist = filepath.Base(parent)
			rel := music.ParseRelease(base)
			// ParseRelease strips a "(1997)" tag; without a " - " it leaves the whole
			// string as Artist, which for an album folder IS the album title.
			f.album, f.year = rel.Artist, rel.Year
			if rel.Album != "" {
				f.album = rel.Album
			}
		}
		if f.album == "" || f.artist == "" {
			f.album, f.artist = strings.TrimSpace(f.album), strings.TrimSpace(f.artist)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// sameDir compares two paths for equality, tolerating separator and trailing-slash noise.
func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
