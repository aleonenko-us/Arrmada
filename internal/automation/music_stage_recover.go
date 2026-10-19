package automation

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// RecoverMusicStaging settles music staging jobs a crash or a restart interrupted (the
// staging-recovery step of MUS-33): copies that never reached the library are deleted, a
// file whose old version is already in the recycle bin is moved into place, files in place
// are recorded, and an interrupted rename is settled file by file. Run at start-up, before
// any import or rename can stage music. It runs whether or not the Music module is on:
// it only finishes what was started.
func (c *Coordinator) RecoverMusicStaging(ctx context.Context) (stage.RecoverReport, error) {
	if c.imp == nil || c.music == nil {
		return stage.RecoverReport{}, nil
	}
	root := c.imp.MusicDir()
	if root == "" {
		return stage.RecoverReport{}, nil
	}
	if _, err := os.Stat(stage.Root(root)); err != nil {
		return stage.RecoverReport{}, nil
	}
	rep, err := stage.Recover(ctx, root, c.stageDeps(), c.recordRecovered)
	if rep.Finished > 0 || rep.RolledBak > 0 {
		c.log.Warn("music: settled interrupted staging jobs", "finished", rep.Finished, "rolled_back", rep.RolledBak, "kept", rep.Kept)
	}
	if rep.Kept > 0 {
		// Only the music folder in use now is looked at: a job left under a folder used
		// before is settled once that folder is the music folder again. Kept jobs delete
		// nothing, and the bin keeps the files they may need back.
		c.log.Error("music: interrupted staging jobs kept — they need a look (nothing in them was deleted)",
			"kept", rep.Kept, "folder", stage.Root(root))
	}
	return rep, err
}

// recordRecovered records one file a crash left in the library but not in the database.
func (c *Coordinator) recordRecovered(ctx context.Context, m stage.Manifest, it stage.Item) error {
	var meta placedMeta
	if len(it.Meta) > 0 {
		if err := json.Unmarshal(it.Meta, &meta); err != nil {
			return err
		}
	}
	switch m.Kind {
	case stage.KindImport:
		if it.Ref == 0 {
			return nil
		}
		if meta.Quality == "" {
			meta.Quality = music.Quality(music.FormatOf(it.Final))
		}
		err := c.recordPlaced(ctx, it.Ref, it.Final, meta)
		if errors.Is(err, music.ErrNotFound) {
			// The track is gone (its artist deleted meanwhile): the file stays where it is,
			// for the library scan to list.
			return nil
		}
		return err
	case stage.KindRetag:
		fi, err := os.Stat(it.Final)
		if err != nil {
			return err
		}
		err = c.music.SetFileRetagged(ctx, it.Final, fi.Size(), fi.ModTime().Unix(), meta.TagsFound)
		if errors.Is(err, music.ErrNotFound) {
			return nil
		}
		return err
	case stage.KindRename:
		if meta.Extra {
			return nil
		}
		err := c.music.MoveFilePath(ctx, it.Source, it.Final)
		if errors.Is(err, music.ErrPathInUse) {
			// A library scan got there first and recorded the file at its new name: the
			// journal is settled; the stale row for the old name is the scan's to clear.
			c.log.Warn("music: a renamed file is already recorded at its new name", "from", it.Source, "to", it.Final)
			return nil
		}
		return err
	}
	return nil
}
