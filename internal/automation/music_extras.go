package automation

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// bringExtras hardlinks a download's extras into the album folder (MUS-36): artwork and
// scans, a booklet, the rip log, cue sheets, lyrics — whatever the allowlist names, from
// the folder the album's audio was in, never over anything already there. A .lrc named
// like a placed track follows the track's new name. Extras are never written, so they're
// linked even while the download seeds (copied across drives).
//
// They only ever come from inside contentRoot, the download's own folder: a single-file
// download saved straight into the downloads folder brings none, rather than its
// neighbours' files. Audio is never an extra, whatever the allowlist says: the album's
// audio is placed as tracks, and a stray copy beside them would be a duplicate.
func (c *Coordinator) bringExtras(al music.Album, contentRoot string, placed []placement, allow map[string]bool) {
	if len(placed) == 0 || len(allow) == 0 || contentRoot == "" {
		return
	}
	if fi, err := os.Stat(contentRoot); err != nil || !fi.IsDir() {
		return
	}
	var dirs []string
	lyrics := map[string]string{}
	for _, p := range placed {
		dirs = append(dirs, filepath.Dir(p.f.Path))
		lyrics[strings.TrimSuffix(p.f.Path, filepath.Ext(p.f.Path))] = strings.TrimSuffix(p.target, filepath.Ext(p.target))
	}
	base := commonDir(dirs)
	albumDir := placed[0].albumDir
	if base == "" || !insideDir(base, contentRoot) || albumDir == "" || !insideDir(albumDir, c.imp.MusicDir()) ||
		filepath.Clean(albumDir) == filepath.Clean(c.imp.MusicDir()) {
		return
	}
	var extras []stage.Extra
	for _, e := range stage.PlanExtras(base, albumDir, allow, lyrics) {
		if !music.IsAudioFile(e.From) {
			extras = append(extras, e)
		}
	}
	if len(extras) == 0 {
		return
	}
	n, skipped := stage.PlaceExtras(extras, c.log)
	if len(n) > 0 || skipped > 0 {
		c.log.Info("music: brought the album's extras along", "album", al.Title, "placed", len(n), "skipped", skipped)
	}
}
