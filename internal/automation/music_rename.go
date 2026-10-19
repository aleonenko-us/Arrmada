package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// Music rename (MUS-34): moving an album's files to the names the music naming templates
// give them now. Never automatic: a preview lists every move, and only the moves a person
// confirmed are made (a move that changed since the preview is skipped). Files, cover,
// extras and lyrics move together; nothing is ever overwritten; each move is journaled
// in a staging manifest, so a crash part way is settled file by file on the next start.
// No file's content is written.

// MusicRenameItem is one proposed move. TrackID 0 is an extra (artwork, a booklet, a
// .lrc) moving with the album. Conflict says why a move can't happen.
type MusicRenameItem struct {
	AlbumID  int64  `json:"album_id"`
	Album    string `json:"album,omitempty"`
	Artist   string `json:"artist,omitempty"`
	TrackID  int64  `json:"track_id,omitempty"`
	From     string `json:"from"`
	To       string `json:"to"`
	Conflict string `json:"conflict,omitempty"`
}

// MusicRenameSkip is a move that wasn't made, and why.
type MusicRenameSkip struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// MusicRenameResult is what a rename did.
type MusicRenameResult struct {
	Moved   int               `json:"renamed"`
	Skipped []MusicRenameSkip `json:"skipped"`
}

const (
	reasonMusicSameTarget = "another file of this album would get the same name"
	reasonMusicChain      = "its new name is another file's that is moving too — rename again once this one is done"
	reasonMusicOwned      = "that name is another track's file"
)

// MusicRenamePlan lists the moves that would put an album's files at their names under
// the current naming, conflicts flagged. Nothing moves.
func (c *Coordinator) MusicRenamePlan(ctx context.Context, albumID int64) ([]MusicRenameItem, error) {
	if c.music == nil || c.imp == nil {
		return nil, errors.New("music isn't set up")
	}
	al, err := c.music.AlbumRecord(ctx, albumID)
	if err != nil {
		return nil, err
	}
	artist, _ := c.music.GetArtist(ctx, al.ArtistID)
	tc, err := c.music.TagContext(ctx, albumID, 0)
	if err != nil {
		return nil, err
	}
	tracks, err := c.music.Tracks(ctx, albumID)
	if err != nil {
		return nil, err
	}
	files, err := c.music.AlbumFiles(ctx, albumID)
	if err != nil {
		return nil, err
	}
	recorded := make(map[string]bool, len(files))
	for _, f := range files {
		recorded[f.Path] = true
	}
	root := c.imp.MusicDir()
	mk := func(trackID int64, from, to string) MusicRenameItem {
		return MusicRenameItem{AlbumID: albumID, Album: al.Title, Artist: artist.Name, TrackID: trackID, From: from, To: to}
	}

	var items []MusicRenameItem
	dirMoves := map[string]map[string]bool{} // old folder → new folders of the album's files in it
	for _, t := range tracks {
		if !t.HasFile || t.FilePath == "" || !insideDir(t.FilePath, root) {
			continue
		}
		if _, err := os.Lstat(t.FilePath); err != nil {
			continue
		}
		facts := music.FileFacts{Quality: music.Quality(t.Format), Codec: t.Codec, BitDepth: t.BitDepth, SampleRate: t.SampleRateHz}
		n, ok := tc.Name(t.ID, facts)
		if !ok {
			continue
		}
		to := c.imp.MusicTrackTarget(n, filepath.Ext(t.FilePath))
		from := t.FilePath
		if dirMoves[filepath.Dir(from)] == nil {
			dirMoves[filepath.Dir(from)] = map[string]bool{}
		}
		dirMoves[filepath.Dir(from)][filepath.Dir(to)] = true
		if to == from {
			continue
		}
		items = append(items, mk(t.ID, from, to))
		// Lyrics named like the track follow its new name.
		lrc := strings.TrimSuffix(from, filepath.Ext(from)) + ".lrc"
		if _, err := os.Lstat(lrc); err == nil && !recorded[lrc] {
			items = append(items, mk(0, lrc, strings.TrimSuffix(to, filepath.Ext(to))+".lrc"))
		}
	}
	// Extras move with their album: when every file of the album in a folder goes to the
	// same new folder and no other audio stays behind, the folder's other files go too.
	moving := map[string]bool{}
	for _, it := range items {
		moving[it.From] = true
	}
	for oldDir, newDirs := range dirMoves {
		if len(newDirs) != 1 {
			continue
		}
		var newDir string
		for d := range newDirs {
			newDir = d
		}
		if newDir == oldDir || oldDir == root {
			continue
		}
		items = append(items, c.extrasMoving(albumID, oldDir, newDir, moving, recorded, mk)...)
	}
	markRenameConflicts(ctx, c, items)
	sort.SliceStable(items, func(i, j int) bool { return items[i].From < items[j].From })
	return items, nil
}

// extrasMoving lists the extra files in oldDir to move into newDir: every file that isn't
// audio, isn't hidden and isn't already moving, keeping its place below oldDir. A folder
// still holding audio that isn't moving (another album's, or a file the library doesn't
// know) keeps its extras: they may be that audio's.
func (c *Coordinator) extrasMoving(albumID int64, oldDir, newDir string, moving, recorded map[string]bool, mk func(int64, string, string) MusicRenameItem) []MusicRenameItem {
	var out []MusicRenameItem
	stay := false
	_ = filepath.WalkDir(oldDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || stay {
			return nil
		}
		if d.IsDir() {
			if p != oldDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() || moving[p] {
			return nil
		}
		if music.IsAudioFile(p) || recorded[p] {
			stay = true // audio staying behind: leave this folder's extras with it
			return nil
		}
		rel, err := filepath.Rel(oldDir, p)
		if err != nil {
			return nil
		}
		out = append(out, mk(0, p, filepath.Join(newDir, rel)))
		return nil
	})
	if stay {
		return nil
	}
	return out
}

// markRenameConflicts flags the moves that can't happen without replacing something:
// a target that exists (and isn't the same file), two moves to one name, a target that is
// another moving file's current name, or a name another track owns.
func markRenameConflicts(ctx context.Context, c *Coordinator, items []MusicRenameItem) {
	sources := map[string]bool{}
	for _, it := range items {
		sources[it.From] = true
	}
	seen := map[string]bool{}
	trackConflict := map[string]bool{} // a track's stem that can't move: its lyrics stay too
	for i := range items {
		it := &items[i]
		switch {
		case seen[it.To]:
			it.Conflict = reasonMusicSameTarget
		case sources[it.To]:
			it.Conflict = reasonMusicChain
		default:
			if fi, err := os.Lstat(it.To); err == nil {
				if si, serr := os.Lstat(it.From); serr != nil || !os.SameFile(si, fi) {
					it.Conflict = reasonTargetExists
				}
			}
		}
		if it.Conflict == "" && it.TrackID > 0 {
			if owner, err := c.music.FileOwner(ctx, it.To); err == nil && owner != 0 && owner != it.TrackID {
				it.Conflict = reasonMusicOwned
			}
		}
		seen[it.To] = true
		if it.Conflict != "" && it.TrackID > 0 {
			trackConflict[strings.TrimSuffix(it.From, filepath.Ext(it.From))] = true
		}
	}
	for i := range items {
		it := &items[i]
		if it.Conflict == "" && it.TrackID == 0 && strings.EqualFold(filepath.Ext(it.From), ".lrc") &&
			trackConflict[strings.TrimSuffix(it.From, filepath.Ext(it.From))] {
			it.Conflict = "its track can't be renamed"
		}
	}
}

// MusicRenamePreview is the dry run over the given albums (nil: every album with files).
func (c *Coordinator) MusicRenamePreview(ctx context.Context, albumIDs []int64) ([]MusicRenameItem, error) {
	if c.music == nil {
		return nil, errors.New("music isn't set up")
	}
	if albumIDs == nil {
		var err error
		if albumIDs, err = c.music.AlbumsWithFiles(ctx); err != nil {
			return nil, err
		}
	}
	var out []MusicRenameItem
	for _, id := range albumIDs {
		items, err := c.MusicRenamePlan(ctx, id)
		if err != nil {
			c.log.Info("music rename: skipped an album that can't be named yet", "album_id", id, "err", err)
			continue
		}
		out = append(out, items...)
	}
	return out, nil
}

// MusicRenameToken names exactly one plan, so an apply can tell the library or the
// scheme changed since the preview.
func MusicRenameToken(items []MusicRenameItem) string {
	lines := make([]string, 0, len(items))
	for _, it := range items {
		lines = append(lines, fmt.Sprintf("%d\x00%s\x00%s\x00%s", it.AlbumID, it.From, it.To, it.Conflict))
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:12])
}

// musicHoldUnpreviewed keeps only the moves a person confirmed, unchanged: a move that
// isn't on the confirmed list, or whose target differs now, stays put; a confirmed move
// that no longer exists (or now conflicts) is reported.
func musicHoldUnpreviewed(plan, only []MusicRenameItem) ([]MusicRenameItem, []MusicRenameSkip) {
	confirmed := make(map[string]string, len(only))
	for _, it := range only {
		confirmed[it.From] = it.To
	}
	planned := make(map[string]MusicRenameItem, len(plan))
	var keep []MusicRenameItem
	skipped := []MusicRenameSkip{}
	for _, it := range plan {
		planned[it.From] = it
		want, ok := confirmed[it.From]
		switch {
		case !ok:
			// Not previewed: it stays where it is.
		case want != it.To:
			skipped = append(skipped, MusicRenameSkip{From: it.From, To: want, Reason: reasonChangedPreview})
		default:
			keep = append(keep, it)
		}
	}
	for _, it := range only {
		if _, ok := planned[it.From]; !ok {
			skipped = append(skipped, MusicRenameSkip{From: it.From, To: it.To, Reason: reasonChangedPreview})
		}
	}
	return keep, skipped
}

// MusicRename moves an album's files to their new names. only is the list the person
// previewed and confirmed; nil applies the whole plan (the library-wide rename, which
// checked its token first). Moves that can't happen are skipped and reported, never
// forced.
func (c *Coordinator) MusicRename(ctx context.Context, albumID int64, only []MusicRenameItem) (MusicRenameResult, error) {
	res := MusicRenameResult{Skipped: []MusicRenameSkip{}}
	if c.music == nil || c.imp == nil {
		return res, errors.New("music isn't set up")
	}
	defer c.lockAlbum(albumID)()
	plan, err := c.MusicRenamePlan(ctx, albumID)
	if err != nil {
		return res, err
	}
	if only != nil {
		var skipped []MusicRenameSkip
		plan, skipped = musicHoldUnpreviewed(plan, only)
		res.Skipped = append(res.Skipped, skipped...)
	}
	var moves []MusicRenameItem
	for _, it := range plan {
		if it.Conflict != "" {
			res.Skipped = append(res.Skipped, MusicRenameSkip{From: it.From, To: it.To, Reason: it.Conflict})
			continue
		}
		moves = append(moves, it)
	}
	if len(moves) == 0 {
		return res, nil
	}
	items := make([]stage.Item, len(moves))
	for i, m := range moves {
		items[i] = stage.Item{Source: m.From, Final: m.To, Ref: m.TrackID}
		if m.TrackID == 0 {
			items[i].Meta, _ = json.Marshal(placedMeta{Extra: true})
		}
	}
	job, err := stage.Start(c.imp.MusicDir(), stage.Manifest{Kind: stage.KindRename, AlbumID: albumID, Items: items}, c.stageDeps())
	if err != nil {
		return res, err
	}
	oldDirs, newDirs := map[string]bool{}, map[string]bool{}
	for i, m := range moves {
		if err := job.Place(i); err != nil {
			_ = job.Discard(i, err.Error())
			res.Skipped = append(res.Skipped, MusicRenameSkip{From: m.From, To: m.To, Reason: err.Error()})
			continue
		}
		if m.TrackID > 0 {
			if err := c.music.MoveFilePath(ctx, m.From, m.To); err != nil {
				// Moved, not yet recorded: the journal keeps it and the next start records it.
				c.log.Warn("music rename: moved a file but couldn't record its new name", "from", m.From, "to", m.To, "err", err)
				res.Moved++
				continue
			}
		}
		_ = job.Recorded(i)
		res.Moved++
		oldDirs[filepath.Dir(m.From)] = true
		newDirs[filepath.Dir(m.To)] = true
	}
	if err := job.Close(); err != nil {
		c.log.Warn("music rename: a rename journal was kept for the next start", "album_id", albumID, "err", err)
	}
	c.pruneEmptyDirs(oldDirs)
	for d := range oldDirs {
		c.libraryChanged("music", d)
	}
	for d := range newDirs {
		c.libraryChanged("music", d)
	}
	if res.Moved > 0 {
		if al, err := c.music.AlbumRecord(ctx, albumID); err == nil {
			c.music.AddEvent(ctx, al.ArtistID, "renamed", fmt.Sprintf("Renamed %d file%s of %q", res.Moved, plural(res.Moved), al.Title))
		}
	}
	return res, nil
}

// pruneEmptyDirs removes folders a rename left empty, and their parents up to the music
// folder (an artist folder whose only album moved). A folder with anything in it stays.
func (c *Coordinator) pruneEmptyDirs(dirs map[string]bool) {
	root := filepath.Clean(c.imp.MusicDir())
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	// Deepest first, so a disc folder goes before its album folder.
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, d := range list {
		for d = filepath.Clean(d); d != root && insideDir(d, root); d = filepath.Dir(d) {
			if entries, err := os.ReadDir(d); err != nil || len(entries) > 0 {
				break
			}
			c.imp.RemoveDirIfEmpty(d)
		}
	}
}
