package automation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/safego"
)

// The verify pass over recorded music files (MUS-15): once a day (and after every library
// scan), each recorded file is checked against what was recorded for it.
//
//   - A file that is there again ends its missing (or not-found) mark.
//   - A file that isn't found is only noted the first time (missing_seen_at). It becomes
//     missing (missing_since) when a pass at least musicMissingGrace later still can't find
//     it. A missing file keeps its row and its track: a remount links it straight back.
//   - Missing is a flag, never a reason to download: the album shows "gone from disk",
//     and the search sweep leaves it alone (Repo.WantedAlbums) until a person asks with
//     "Search for missing tracks". The file may only be renamed, or on a disk that's away;
//     movies work the same way (movies/missing.go).
//   - A file whose size or modification time changed outside Arrmada is measured again
//     (ffprobe at idle priority) and its house-tags state rechecked.
//
// Never deleted, never un-recorded. And never en masse: when the folder a missing file
// should be in looks unmounted — the music folder empty or gone, or the nearest folder
// still there empty or on another device than the file was (a share mounted on a subfolder
// that went away) — or when more files vanish in one pass than a deletion would explain,
// nothing is marked at all. Either way the pass says so as a health warning instead.

// musicMissingGrace is how long a file must stay unfound before it counts as missing: a
// pass a day means it was gone on two passes a day apart.
const musicMissingGrace = 20 * time.Hour

// musicChangedPerPass caps the changed files one pass measures again (each is an ffprobe at
// idle priority, plus a tag read): a library retagged by another tool catches up over a few
// passes, the rest staying marked as unmeasured until then.
const musicChangedPerPass = 500

// musicMissingThreshold is how many files may newly vanish in one pass before the pass
// decides it's looking at an unmounted share rather than deletions: 5% of the files
// checked, and never fewer than 20 (the movie check's threshold).
func musicMissingThreshold(checked int) int { return max(20, checked*5/100) }

// MusicVerifyResult is what one verify pass did.
type MusicVerifyResult struct {
	At       time.Time `json:"at"`
	Checked  int       `json:"checked"`   // recorded files looked at
	NotFound int       `json:"not_found"` // not found for the first time: marked, not missing yet
	Missing  int       `json:"missing"`   // missing after the grace period: flagged as gone from disk, never searched by themselves
	Back     int       `json:"back"`      // found again
	Changed  int       `json:"changed"`   // changed outside Arrmada: measured again
	Skipped  int       `json:"skipped"`   // couldn't be judged (unreadable, or under a folder that looks unmounted)
	// Refused is set when the pass marked nothing because the music folder looked unmounted
	// or too many files vanished at once: the health warning's text.
	Refused string `json:"refused,omitempty"`
	// Away are folders that look empty or unmounted (a share on a subfolder that's away, an
	// album folder emptied but left behind): their files weren't checked this pass; the
	// rest of the library was.
	Away []string `json:"away,omitempty"`
}

// musicAwayListed caps the folders the health note names.
const musicAwayListed = 5

// Warning is the pass's health note ("" when all was well): the refusal, else the folders
// whose files weren't checked.
func (r MusicVerifyResult) Warning() string {
	if r.Refused != "" || len(r.Away) == 0 {
		return r.Refused
	}
	names := r.Away
	more := ""
	if len(names) > musicAwayListed {
		more = fmt.Sprintf(" and %d more", len(names)-musicAwayListed)
		names = names[:musicAwayListed]
	}
	return fmt.Sprintf("These music folders look empty or unmounted, so their files weren't checked: %s%s. The rest of the library was. If a share or disk is away, mount it; if you emptied a folder on purpose, remove it.",
		strings.Join(names, ", "), more)
}

// musicVerifyState holds the last pass's outcome for the health check, and a test seam.
type musicVerifyState struct {
	mu   sync.Mutex
	last MusicVerifyResult
	// houseFn checks a changed file's house tags (nil: houseTagsHold on the real file).
	houseFn func(tc music.TagContext, trackID int64, path string) (hold, ok bool)
}

// LastMusicVerify is the most recent verify pass's outcome (zero before the first).
func (c *Coordinator) LastMusicVerify() MusicVerifyResult {
	c.musicVerify.mu.Lock()
	defer c.musicVerify.mu.Unlock()
	return c.musicVerify.last
}

// MusicFilesWarning is the health warning from the last verify pass ("" when all was well).
func (c *Coordinator) MusicFilesWarning() string { return c.LastMusicVerify().Warning() }

// verifyStat is one recorded file's look at the disk.
type verifyStat struct {
	fi  fs.FileInfo
	err error
}

// VerifyMusicFiles is the verify pass (see the top of this file). A pass cut short by ctx
// writes nothing.
func (c *Coordinator) VerifyMusicFiles(ctx context.Context) (MusicVerifyResult, error) {
	out := MusicVerifyResult{At: time.Now()}
	if c.music == nil || c.imp == nil {
		return out, nil
	}
	if !c.moduleOn(ctx, "music") {
		return out, ErrModuleOff
	}
	rows, err := c.music.VerifyRows(ctx)
	if err != nil {
		return out, err
	}
	root := filepath.Clean(c.imp.MusicDir())

	// One stat per file, a few at a time: on a network share each is a round trip.
	stats := make([]verifyStat, len(rows))
	workers := c.music.ScanWorkers()
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		safego.Go(c.log, "music-verify-stat", func() {
			defer wg.Done()
			for i := range next {
				fi, err := os.Stat(rows[i].Path)
				stats[i] = verifyStat{fi, err}
			}
		})
	}
	for i := range rows {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return out, err // a partial pass decides nothing
	}

	mounted := map[string]bool{}
	anchorOK := func(dir string) bool {
		ok, seen := mounted[dir]
		if !seen {
			ok = library.FolderLooksMounted(dir)
			mounted[dir] = ok
		}
		return ok
	}
	devs := map[string]string{} // folder → its device ("" when it can't be read)
	// A reboot or remount can renumber a filesystem's device (ZFS, FUSE/shfs, NFS, SMB,
	// btrfs): what the files still there say maps each old number to the new one, so a
	// missing file recorded under the old number isn't read as "on another disk".
	remap := deviceRemap(rows, stats)
	w := music.VerifyWrites{At: out.At}
	var changed []int             // row indexes
	unmounted := map[string]int{} // the music folder (or an outside file's anchor) looks unmounted
	away := map[string]int{}      // a nearer folder looks empty or unmounted: its files are skipped
	gone := map[int64][]int{}     // album → rows newly missing
	back := map[int64]int{}       // album → files seen again
	backRow := map[int64]int{}
	for i, r := range rows {
		out.Checked++
		st := stats[i]
		base := music.VerifyBaseline{ID: r.ID, Path: r.Path}
		switch {
		case st.err == nil:
			base.Size, base.Mtime, base.FileID = st.fi.Size(), st.fi.ModTime().Unix(), music.FileIdentity(st.fi)
			if r.MissingSince != "" || r.NotFoundSince != "" {
				w.Back = append(w.Back, base)
				back[r.AlbumID]++
				backRow[r.AlbumID] = i
			}
			switch {
			case r.MtimeUnix == 0 || r.SizeBytes == 0:
				// Recorded without a size or time (an upgraded library, an old scan): this
				// is the file as it is, not a change.
				w.Baseline = append(w.Baseline, base)
			case base.Size != r.SizeBytes || base.Mtime != r.MtimeUnix:
				changed = append(changed, i)
			case base.FileID != r.FileID && base.FileID != "":
				w.Baseline = append(w.Baseline, base) // same file, inode not yet recorded
			}
		case errors.Is(st.err, fs.ErrNotExist):
			anchor := verifyAnchor(root, r.Path)
			fileID := remapDevice(r.FileID, remap)
			if fileID != r.FileID {
				w.Redevice = append(w.Redevice, music.VerifyBaseline{ID: r.ID, Path: r.Path, FileID: fileID})
			}
			nearest, looksAway := nearestLooksAway(r.Path, fileID, anchorOK, devs)
			switch {
			case !anchorOK(anchor):
				unmounted[anchor]++
				out.Skipped++
			case looksAway:
				// Only this folder's files wait: the rest of the library is still checked.
				away[nearest]++
				out.Skipped++
			case r.MissingSince != "":
				// Already missing; stays so until it's back.
			case r.NotFoundSince == "":
				w.NotFound = append(w.NotFound, base)
			case notFoundLongEnough(r.NotFoundSince, out.At):
				w.Missing = append(w.Missing, base)
				gone[r.AlbumID] = append(gone[r.AlbumID], i)
			}
		default:
			out.Skipped++ // unreadable is not gone
		}
	}

	switch {
	case len(unmounted) > 0:
		dirs := make([]string, 0, len(unmounted))
		for d := range unmounted {
			dirs = append(dirs, d)
		}
		sort.Strings(dirs)
		out.Refused = fmt.Sprintf("The music folder %s looks unmounted (it's missing or empty), so the music file check marked nothing. Check the share or the array is mounted, then run the check again from Settings → Status → Tasks.", strings.Join(dirs, ", "))
	case len(w.NotFound)+len(w.Missing) > musicMissingThreshold(out.Checked):
		out.Refused = fmt.Sprintf("%d music files vanished since the last check — more than a deletion would explain — so nothing was marked. Part of the array or a share may be unmounted; check it, then run the check again from Settings → Status → Tasks.", len(w.NotFound)+len(w.Missing))
	}
	if out.Refused != "" {
		// Nothing is marked gone while any of it looks unmounted. Files seen again are
		// still cleared: they were just seen on disk.
		out.Skipped += len(w.NotFound) + len(w.Missing)
		w.NotFound, w.Missing, gone = nil, nil, nil
		c.log.Warn("music folder looks unmounted — the music file check marked nothing", "unmounted", unmounted, "checked", out.Checked, "skipped", out.Skipped)
	}
	for d := range away {
		out.Away = append(out.Away, d)
	}
	sort.Strings(out.Away)
	if len(out.Away) > 0 {
		c.log.Warn("music file check: folders look empty or unmounted — their files weren't checked", "folders", out.Away)
	}

	// Changed files: measured again, their tags rechecked. Outside the transaction: a probe
	// can take a while.
	if len(changed) > musicChangedPerPass {
		changed = changed[:musicChangedPerPass]
	}
	tcs := map[int64]*music.TagContext{}
	for _, i := range changed {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		r, st := rows[i], stats[i]
		ch := music.VerifyChange{
			VerifyBaseline: music.VerifyBaseline{ID: r.ID, Path: r.Path, Size: st.fi.Size(), Mtime: st.fi.ModTime().Unix(), FileID: music.FileIdentity(st.fi)},
			TagState:       r.TagState,
		}
		if info, err := c.probeAudioIdle(ctx, r.Path); err == nil {
			if q := music.TierFromProbe(info); q != music.QualityUnknown {
				ch.Probe, ch.Tier = &info, q
			}
		}
		if r.TrackID > 0 {
			ch.TagState = c.recheckTagState(ctx, tcs, r)
		}
		w.Changed = append(w.Changed, ch)
	}

	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	applied, err := c.music.ApplyVerify(wctx, w)
	if err != nil {
		return out, err
	}
	out.Back, out.NotFound, out.Missing, out.Changed = applied.Back, applied.NotFound, applied.Missing, applied.Changed

	// One history line per album. Nothing is searched: the files may only be renamed or
	// on a disk that's away (the album page offers the search, as an explicit choice).
	for _, idx := range gone {
		r := rows[idx[0]]
		c.music.AddEvent(wctx, r.ArtistID, "missing",
			fmt.Sprintf("%d track file(s) of %q are gone from disk — not searched for by itself; the album page can search for them", len(idx), r.AlbumTitle))
	}
	for albumID, n := range back {
		r := rows[backRow[albumID]]
		c.music.AddEvent(wctx, r.ArtistID, "detected", fmt.Sprintf("%d track file(s) of %q are back on disk", n, r.AlbumTitle))
	}
	if out.NotFound+out.Missing+out.Back+out.Changed > 0 {
		c.log.Info("music file check", "checked", out.Checked, "not_found", out.NotFound, "missing", out.Missing,
			"back", out.Back, "changed", out.Changed, "skipped", out.Skipped)
		if c.bus != nil {
			c.bus.Publish("music.files.checked", map[string]any{"missing": out.Missing, "back": out.Back, "changed": out.Changed})
		}
	}
	c.musicVerify.mu.Lock()
	c.musicVerify.last = out
	c.musicVerify.mu.Unlock()
	return out, nil
}

// recheckTagState is a changed file's tag state: house when it still carries exactly the
// house tags of its track, as found when it doesn't, and what it was when that can't be
// told.
func (c *Coordinator) recheckTagState(ctx context.Context, tcs map[int64]*music.TagContext, r music.VerifyRow) string {
	tc, ok := tcs[r.AlbumID]
	if !ok {
		if got, err := c.music.TagContext(ctx, r.AlbumID, 0); err == nil {
			tc = &got
		}
		tcs[r.AlbumID] = tc
	}
	if tc == nil {
		return r.TagState
	}
	check := c.musicVerify.houseFn
	if check == nil {
		check = houseTagsHold
	}
	hold, known := check(*tc, r.TrackID, r.Path)
	switch {
	case !known:
		return r.TagState
	case hold:
		return music.TagStateHouse
	}
	return music.TagStateAsFound
}

// notFoundLongEnough reports whether a file first not found at since has stayed unfound
// for the grace period. An unreadable time never counts: the file waits.
func notFoundLongEnough(since string, now time.Time) bool {
	t := parseTime(since)
	return !t.IsZero() && now.Sub(t) >= musicMissingGrace
}

// nearestLooksAway finds the nearest folder above a missing file that is still there, and
// reports whether it looks like a mount point whose disk is away: empty, or on another
// device than the file was recorded on (fileID is "dev:ino"). A share mounted on a
// subfolder of the music folder leaves exactly that behind when it drops: its mount point,
// empty, on the parent's disk. mounted and devs cache the looks per folder.
func nearestLooksAway(path, fileID string, mounted func(string) bool, devs map[string]string) (string, bool) {
	dir := filepath.Dir(filepath.Clean(path))
	for {
		if _, seen := devs[dir]; !seen {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				devs[dir] = deviceOf(music.FileIdentity(fi))
			} else {
				devs[dir] = "\x00gone"
			}
		}
		if devs[dir] != "\x00gone" {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir, true // nothing above it is there at all
		}
		dir = parent
	}
	if !mounted(dir) {
		return dir, true
	}
	if want := deviceOf(fileID); want != "" && devs[dir] != "" && devs[dir] != want {
		return dir, true
	}
	return dir, false
}

// deviceRemap learns, from the files still on disk, which device each recorded device
// number is now: recorded "7:…" seen as "9:…" maps 7 to 9. A number seen as two different
// devices is ambiguous and left out.
func deviceRemap(rows []music.VerifyRow, stats []verifyStat) map[string]string {
	seen := map[string]map[string]bool{}
	for i, r := range rows {
		if stats[i].err != nil {
			continue
		}
		was, now := deviceOf(r.FileID), deviceOf(music.FileIdentity(stats[i].fi))
		if was == "" || now == "" || was == now {
			continue
		}
		if seen[was] == nil {
			seen[was] = map[string]bool{}
		}
		seen[was][now] = true
	}
	out := map[string]string{}
	for was, nows := range seen {
		if len(nows) == 1 {
			for now := range nows {
				out[was] = now
			}
		}
	}
	return out
}

// remapDevice rewrites a file identity's device through the remap.
func remapDevice(fileID string, remap map[string]string) string {
	dev, ino, ok := strings.Cut(fileID, ":")
	if !ok {
		return fileID
	}
	if now, ok := remap[dev]; ok {
		return now + ":" + ino
	}
	return fileID
}

// deviceOf is the device part of a "dev:ino" file identity ("" when there is none).
func deviceOf(fileID string) string {
	dev, _, ok := strings.Cut(fileID, ":")
	if !ok {
		return ""
	}
	return dev
}

// verifyAnchor is the folder that has to be there for a missing file to count as missing:
// the music folder, for a file inside it; else the folder holding the file's album folder
// (a file left in an old music folder).
func verifyAnchor(root, path string) string {
	if root != "" && root != "." && within(root, path) {
		return root
	}
	return filepath.Dir(filepath.Dir(path))
}
