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
//     it; only then does its album count the track as gone, and want it again. A missing
//     file keeps its row and its track: a remount links it straight back.
//   - A file whose size or modification time changed outside Arrmada is measured again
//     (ffprobe at idle priority) and its house-tags state rechecked.
//
// Never deleted, never un-recorded. And never en masse: when the music folder (or the
// folder a file outside it sits in) looks unmounted, missing or empty, files under it are
// left alone, and when more files vanish in one pass than a deletion would explain, nothing
// is marked at all. Either way the pass says so as a health warning instead (the movie
// missing-file check's guard, library.FolderLooksMounted, reused).

// musicMissingGrace is how long a file must stay unfound before it counts as missing: a
// pass a day means it was gone on two passes a day apart.
const musicMissingGrace = 20 * time.Hour

// musicChangedPerPass caps the changed files one pass measures again (each is an ffprobe at
// idle priority, plus a tag read): a library retagged by another tool catches up over a few
// passes, the rest staying marked as unmeasured until then.
const musicChangedPerPass = 500

// musicMissingThreshold is how many files may newly vanish in one pass before the pass
// decides it's looking at an unmounted share rather than deletions: a fifth of the files
// checked, and never fewer than 20.
func musicMissingThreshold(checked int) int { return max(20, checked/5) }

// MusicVerifyResult is what one verify pass did.
type MusicVerifyResult struct {
	At       time.Time `json:"at"`
	Checked  int       `json:"checked"`   // recorded files looked at
	NotFound int       `json:"not_found"` // not found for the first time: marked, not missing yet
	Missing  int       `json:"missing"`   // missing after the grace period: their tracks are wanted again
	Back     int       `json:"back"`      // found again
	Changed  int       `json:"changed"`   // changed outside Arrmada: measured again
	Skipped  int       `json:"skipped"`   // couldn't be judged (unreadable, or under a folder that looks unmounted)
	// Refused is set when the pass marked nothing because the library looked unmounted: the
	// health warning's text.
	Refused string `json:"refused,omitempty"`
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
func (c *Coordinator) MusicFilesWarning() string { return c.LastMusicVerify().Refused }

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
	w := music.VerifyWrites{At: out.At}
	var changed []int // row indexes
	unmounted := map[string]int{}
	gone := map[int64][]int{} // album → rows newly missing
	back := map[int64]int{}   // album → files seen again
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
			switch {
			case !anchorOK(anchor):
				unmounted[anchor]++
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
		out.Refused = fmt.Sprintf("The music folder %s looks unmounted (it's missing or empty), so the music file check marked nothing there. Check the share or the array is mounted, then run the check again from Settings → Status → Tasks.", strings.Join(dirs, ", "))
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

	// One history line per album, and its search backoff cleared: wanted again, promptly.
	var albums []int64
	for albumID, idx := range gone {
		albums = append(albums, albumID)
		r := rows[idx[0]]
		c.music.AddEvent(wctx, r.ArtistID, "missing",
			fmt.Sprintf("%d track file(s) of %q are gone from disk, wanted again", len(idx), r.AlbumTitle))
	}
	if err := c.music.ClearSearchMisses(wctx, albums); err != nil {
		c.log.Warn("music file check: couldn't clear the search backoff of albums with missing tracks", "err", err)
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

// verifyAnchor is the folder that has to be there for a missing file to count as missing:
// the music folder, for a file inside it; else the folder holding the file's album folder
// (a file left in an old music folder).
func verifyAnchor(root, path string) string {
	if root != "" && root != "." && within(root, path) {
		return root
	}
	return filepath.Dir(filepath.Dir(path))
}
