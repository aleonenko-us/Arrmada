package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// MusicRetagResult is what retagging an album did.
type MusicRetagResult struct {
	Retagged int      `json:"retagged"`
	Skipped  int      `json:"skipped"` // files the tag library can't write (left as they are)
	Failed   int      `json:"failed"`
	Notes    []string `json:"notes,omitempty"`
}

// RetagAlbum rewrites an album's files in the house style (MUS-33): each file is copied
// into staging, tagged, checked, and then swapped in, the original going to the recycle
// bin. A library file is never written in place, so one hardlinked to a torrent that is
// still seeding keeps its bytes: the bin takes the library's name for it, and the torrent's
// own name and content are untouched. Nothing is swapped when the bin can't take the
// original.
func (c *Coordinator) RetagAlbum(ctx context.Context, albumID int64) (MusicRetagResult, error) {
	var res MusicRetagResult
	if c.music == nil || c.imp == nil {
		return res, errors.New("music isn't set up")
	}
	defer c.lockAlbum(albumID)()
	al, err := c.music.AlbumRecord(ctx, albumID)
	if err != nil {
		return res, err
	}
	tc, err := c.music.TagContext(ctx, albumID, 0)
	if err != nil {
		return res, err
	}
	tracks, err := c.music.Tracks(ctx, albumID)
	if err != nil {
		return res, err
	}
	root := c.imp.MusicDir()
	var items []stage.Item
	for _, t := range tracks {
		if !t.HasFile || t.FilePath == "" {
			continue
		}
		if _, _, ok := tc.Track(t.ID); !ok {
			continue
		}
		if !insideDir(t.FilePath, root) {
			// Staging lives on the music folder's drive; a file elsewhere isn't Arrmada's to swap.
			res.Skipped++
			res.Notes = append(res.Notes, filepath.Base(t.FilePath)+": outside the music folder")
			continue
		}
		if _, err := os.Stat(t.FilePath); err != nil {
			res.Failed++
			res.Notes = append(res.Notes, filepath.Base(t.FilePath)+": not on disk")
			continue
		}
		items = append(items, stage.Item{Source: t.FilePath, Final: t.FilePath, Replace: t.FilePath, Ref: t.ID})
	}
	if len(items) == 0 {
		return res, nil
	}
	job, err := stage.Start(root, stage.Manifest{Kind: stage.KindRetag, AlbumID: albumID, Items: items}, c.stageDeps())
	if err != nil {
		return res, err
	}
	if err := job.CheckSpace(); err != nil {
		_ = job.Abandon("no space")
		return res, err
	}
	var embed *audiotag.Picture
	if cover := c.frontCover(ctx, tc, 500); cover != nil {
		embed = &audiotag.Picture{Data: cover}
	}
	var done []string // the folders of the files retagged
	for i, it := range items {
		name := filepath.Base(it.Final)
		if err := job.Stage(i); err != nil {
			res.Failed++
			res.Notes = append(res.Notes, name+": "+err.Error())
			_ = job.Discard(i, err.Error())
			continue
		}
		found, foundJSON := c.tagsFoundJSON(it.Source)
		tags, _ := tc.HouseTags(it.Ref, found)
		if err := job.Tag(ctx, i, tags, embed); err != nil {
			if errors.Is(err, audiotag.ErrUnsupported) {
				res.Skipped++
				continue
			}
			res.Failed++
			res.Notes = append(res.Notes, name+": "+err.Error())
			continue
		}
		_ = setMeta(job, i, placedMeta{TagState: music.TagStateHouse, TagsFound: foundJSON})
		if err := job.Place(i); err != nil {
			if serr := job.Settle(i); serr != nil {
				c.log.Error("music retag: the original is in the recycle bin and the new copy is still in staging — it's finished at the next start", "file", it.Final, "err", serr)
			}
			if job.Items()[i].State != stage.Placed {
				res.Failed++
				res.Notes = append(res.Notes, name+": "+err.Error())
				continue
			}
		}
		fi, err := os.Stat(it.Final)
		if err == nil {
			err = c.music.SetFileRetagged(ctx, it.Final, fi.Size(), fi.ModTime().Unix(), foundJSON)
		}
		if err != nil {
			res.Failed++
			res.Notes = append(res.Notes, name+": retagged, but recording it failed (finished at the next start)")
			continue
		}
		_ = job.Recorded(i)
		res.Retagged++
		done = append(done, filepath.Dir(it.Final))
	}
	if dir := commonDir(done); dir != "" && insideDir(dir, root) && dir != filepath.Clean(root) {
		if big := c.frontCover(ctx, tc, 1200); big != nil && music.IsJPEG(big) {
			if _, err := job.PlaceCover(dir, big); err != nil {
				c.log.Warn("music retag: couldn't write cover.jpg", "album", al.Title, "err", err)
			}
		}
		c.libraryChanged("music", dir)
	}
	if err := job.Abandon("retag finished"); err != nil {
		c.log.Warn("music retag: a staging job was kept for the next start", "album", al.Title, "err", err)
	}
	if res.Retagged > 0 {
		c.music.AddEvent(ctx, al.ArtistID, "retagged", fmt.Sprintf(
			"Wrote house tags into %d file(s) of %q; the originals are in the recycle bin", res.Retagged, al.Title))
	}
	return res, nil
}
