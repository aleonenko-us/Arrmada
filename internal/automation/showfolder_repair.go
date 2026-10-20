package automation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Putting misplaced episode files back (#56).
//
// Before the fix, a show whose season folders weren't called "Season N" had its next
// imports put in <TV folder>/<its season folder's name>/Season N/… — <TV>/S13/Season 12 —
// shared with every other show that had an "S13". This moves those files back into their
// show's folder. It is always preview first, then apply exactly what was previewed, and it
// never replaces a file: where the show's folder already has a copy of the episode, the
// misplaced copy goes to the recycle bin only when it's the same file or a lower quality
// one, and otherwise both are left for the person to choose.

// What happens to one misplaced file.
const (
	MisplacedMove    = "move"    // back into the show's folder
	MisplacedRecycle = "recycle" // the show's folder already has this episode, as good or better
	MisplacedLeave   = "leave"   // can't be decided here: left where it is, for the person
)

// MisplacedItem is one misplaced episode file and what putting it back does with it.
type MisplacedItem struct {
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Action  string `json:"action"`
	// Existing is the copy of the episode already in the show's folder, if any.
	Existing string `json:"existing,omitempty"`
	// Note says why the file is misplaced, or why it's recycled or left.
	Note string `json:"note,omitempty"`
}

// MisplacedPlan is a show's misplaced files and where they go.
type MisplacedPlan struct {
	// ShowFolder is where they go: the show's folder, or (NewFolder) a new one named by
	// the naming scheme when none of its files are in a folder of its own.
	ShowFolder string `json:"show_folder"`
	NewFolder  bool   `json:"new_folder,omitempty"`
	// Why is set when nothing can go back, and says why.
	Why   string          `json:"why,omitempty"`
	Items []MisplacedItem `json:"items"`
}

// MisplacedResult is what putting the files back did.
type MisplacedResult struct {
	Moved    int          `json:"moved"`
	Recycled int          `json:"recycled"`
	Skipped  []RenameSkip `json:"skipped"`
	// RemovedFolders are the misplaced folders left empty and removed.
	RemovedFolders []string `json:"removed_folders"`
}

// MisplacedPreview works out where each of the show's misplaced episode files goes. It
// changes nothing.
func (c *Coordinator) MisplacedPreview(ctx context.Context, seriesID int64) (MisplacedPlan, error) {
	plan := MisplacedPlan{Items: []MisplacedItem{}}
	if c.series == nil || c.imp == nil {
		return plan, fmt.Errorf("series module not ready")
	}
	s, err := c.series.Get(ctx, seriesID)
	if err != nil {
		return plan, err
	}
	info, files, err := c.series.MisplacedFiles(ctx, seriesID)
	if err != nil {
		return plan, err
	}
	if len(files) == 0 {
		return plan, nil
	}
	imp := c.imp.InTV(c.series.ImportDirOf(ctx, s, info)).Using(ctx)
	target := info.Path
	if target == "" {
		target = imp.SeriesFolderPath("", s.Title, s.Year)
		_, statErr := os.Stat(target)
		plan.NewFolder = errors.Is(statErr, os.ErrNotExist)
	}
	plan.ShowFolder = target
	why := map[string]string{}
	for _, f := range files {
		why[f.Path] = f.Why
	}
	unsafe := c.series.UnsafeImportFolder(ctx, s, target)
	if unsafe != "" {
		plan.Why = fmt.Sprintf("There's nowhere safe to put them: %s.", unsafe)
	}
	binOn := true
	if _, err := c.bin().For(files[0].Path); errors.Is(err, library.ErrRecycleDisabled) {
		binOn = false
	}
	seen := map[string]bool{}
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			if !e.HasFile || why[e.FilePath] == "" || seen[e.FilePath] {
				continue
			}
			seen[e.FilePath] = true // a multi-episode file is one item, under its first episode
			it := MisplacedItem{From: e.FilePath, Season: e.SeasonNumber, Episode: e.EpisodeNumber, Action: MisplacedMove, Note: why[e.FilePath]}
			if plan.Why != "" {
				it.Action, it.Note = MisplacedLeave, plan.Why
				plan.Items = append(plan.Items, it)
				continue
			}
			seasonDir := filepath.Join(target, imp.SeasonDirIn(target, s.Title, s.Year, e.SeasonNumber))
			it.To = filepath.Join(seasonDir, filepath.Base(e.FilePath))
			if existing := episodeCopyIn(seasonDir, it.To, e.FilePath, e.SeasonNumber, e.EpisodeNumber); existing != "" {
				it.Existing = existing
				it.Action, it.Note = c.misplacedCopyVerdict(ctx, e.FilePath, e.SourceRelease, e.SizeBytes, existing, binOn)
			}
			plan.Items = append(plan.Items, it)
		}
	}
	sort.SliceStable(plan.Items, func(i, j int) bool {
		if plan.Items[i].Season != plan.Items[j].Season {
			return plan.Items[i].Season < plan.Items[j].Season
		}
		return plan.Items[i].Episode < plan.Items[j].Episode
	})
	return plan, nil
}

// episodeCopyIn is a copy of the episode already in seasonDir ("" when there's none): the
// file at to, unless it's the misplaced file itself under another name (a hardlink), or
// another video whose name numbers the same episode.
func episodeCopyIn(seasonDir, to, from string, season, episode int) string {
	if renameOccupied(from, to) {
		return to
	}
	entries, err := os.ReadDir(seasonDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !library.IsVideoFile(e.Name()) {
			continue
		}
		p := filepath.Join(seasonDir, e.Name())
		if p == to {
			continue // checked above: it's the same file
		}
		rel := parser.Parse(e.Name())
		if rel.Season != season {
			continue
		}
		for _, ep := range rel.Episodes {
			if ep == episode {
				return p
			}
		}
	}
	return ""
}

// misplacedCopyVerdict decides between a misplaced file and the copy of its episode the
// show's folder already has: the misplaced one goes to the recycle bin when it's the same
// file or a lower quality one, and both stay — for the person to choose — otherwise.
func (c *Coordinator) misplacedCopyVerdict(ctx context.Context, from, release string, size int64, existing string, binOn bool) (string, string) {
	fi, ferr := os.Stat(from)
	ei, eerr := os.Stat(existing)
	if ferr != nil || eerr != nil {
		return MisplacedLeave, "Couldn't read both copies, so neither is touched."
	}
	if os.SameFile(fi, ei) {
		return MisplacedRecycle, "The show's folder already has this very file (" + filepath.Base(existing) + "); the extra name goes to the recycle bin."
	}
	note := ""
	if fi.Size() == ei.Size() && sameContent(from, existing) {
		note = "An identical copy is already in the show's folder (" + filepath.Base(existing) + "); this one goes to the recycle bin."
	} else if mine, theirs := c.copyResolution(ctx, from, release, size), c.copyResolution(ctx, existing, "", ei.Size()); mine > 0 && theirs > 0 && mine < theirs {
		note = "A better copy is already in the show's folder (" + filepath.Base(existing) + "); this lower-quality one goes to the recycle bin."
	}
	if note == "" {
		return MisplacedLeave, "The show's folder already has another copy of this episode (" + filepath.Base(existing) + ") and it isn't clear which is better — both are left for you to choose."
	}
	if !binOn {
		return MisplacedLeave, "The show's folder already has a copy as good or better (" + filepath.Base(existing) + "), but the recycle bin is off, so nothing is removed — delete the one you don't want yourself."
	}
	return MisplacedRecycle, note
}

// copyResolution ranks a file's resolution: its probed one when Convert has looked at it,
// else what its name (or its recorded release) says. 0 = unknown.
func (c *Coordinator) copyResolution(ctx context.Context, path, release string, size int64) int {
	if f := c.factsFor(ctx, path, size); f != nil && f.Resolution != "" {
		if f.Resolution == "SD" {
			return parser.ResolutionRank(parser.Res480p)
		}
		return parser.ResolutionRank(parser.Resolution(f.Resolution))
	}
	if r := parser.Parse(filepath.Base(path)).StatedResolution(); r != "" {
		return parser.ResolutionRank(r)
	}
	if release != "" {
		if r := parser.Parse(release).StatedResolution(); r != "" {
			return parser.ResolutionRank(r)
		}
	}
	return 0
}

// sameContent reports whether two files of the same size hold the same bytes, judged by
// their start, middle and end (1 MiB each): enough to tell two different encodes apart
// without reading two whole episodes, and only ever used to recycle the second copy of a
// file whose twin stays in place.
func sameContent(a, b string) bool {
	fa, err := os.Open(a)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()
	sa, err := fa.Stat()
	if err != nil {
		return false
	}
	const chunk = 1 << 20
	size := sa.Size()
	offsets := []int64{0, size / 2, size - chunk}
	bufA, bufB := make([]byte, chunk), make([]byte, chunk)
	for _, off := range offsets {
		if off < 0 {
			off = 0
		}
		na, errA := fa.ReadAt(bufA, off)
		nb, errB := fb.ReadAt(bufB, off)
		if (errA != nil && !errors.Is(errA, io.EOF)) || (errB != nil && !errors.Is(errB, io.EOF)) || na != nb {
			return false
		}
		if !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false
		}
	}
	return true
}

// MoveMisplacedBack applies a previewed MisplacedPlan: exactly the items the person
// confirmed (only), each only if the plan still says the same thing for it — anything
// that changed since the preview is skipped and reported, never applied blind. Moves go
// through the rename machinery (never replacing a file, subtitles alongside); recycles go
// through the recycle bin and point the episode at the copy that stays. Folders the
// misplaced files leave are removed only when empty. The show's History records it all.
func (c *Coordinator) MoveMisplacedBack(ctx context.Context, seriesID int64, only []MisplacedItem) (MisplacedResult, error) {
	res := MisplacedResult{Skipped: []RenameSkip{}, RemovedFolders: []string{}}
	plan, err := c.MisplacedPreview(ctx, seriesID)
	if err != nil {
		return res, err
	}
	s, err := c.series.Get(ctx, seriesID)
	if err != nil {
		return res, err
	}
	planned := map[string]MisplacedItem{}
	for _, it := range plan.Items {
		planned[it.From] = it
	}
	var moves, recycles []MisplacedItem
	for _, want := range only {
		it, ok := planned[want.From]
		if !ok || it.To != want.To || it.Action != want.Action || it.Existing != want.Existing {
			res.Skipped = append(res.Skipped, RenameSkip{From: want.From, To: want.To, Season: want.Season, Episode: want.Episode, Reason: reasonChangedPreview})
			continue
		}
		switch it.Action {
		case MisplacedMove:
			moves = append(moves, it)
		case MisplacedRecycle:
			recycles = append(recycles, it)
		}
	}

	// Moves: every episode row on a file moves with it.
	target := map[string]string{}
	for _, it := range moves {
		target[it.From] = it.To
	}
	var rows []renameRow
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			if to, ok := target[e.FilePath]; ok && e.HasFile {
				rows = append(rows, renameRow{Season: e.SeasonNumber, Episode: e.EpisodeNumber, Path: e.FilePath, Target: to, Size: e.SizeBytes})
			}
		}
	}
	steps, conflicts := planSeriesRename(rows, renameOccupied)
	res.Skipped = append(res.Skipped, conflicts...)
	moved, skipped, _ := c.runRenameSteps(ctx, seriesID, steps)
	res.Moved = len(moved)
	res.Skipped = append(res.Skipped, skipped...)
	left := map[string]bool{}
	var lines []string
	for _, st := range moved {
		left[filepath.Dir(st.From)] = true
		lines = append(lines, fmt.Sprintf("S%02dE%02d moved back from %s", st.Rows[0].Season, st.Rows[0].Episode, filepath.Dir(st.From)))
	}

	// Recycles: the copy in the show's folder stays, and the episode points at it.
	for _, it := range recycles {
		if err := c.recycleMisplaced(ctx, s, it); err != nil {
			res.Skipped = append(res.Skipped, RenameSkip{From: it.From, To: it.Existing, Season: it.Season, Episode: it.Episode, Reason: err.Error()})
			continue
		}
		res.Recycled++
		left[filepath.Dir(it.From)] = true
		lines = append(lines, fmt.Sprintf("S%02dE%02d: the misplaced copy in %s went to the recycle bin; %s stays", it.Season, it.Episode, filepath.Dir(it.From), filepath.Base(it.Existing)))
	}

	res.RemovedFolders = c.pruneLeftFolders(left)
	if res.Moved > 0 || res.Recycled > 0 {
		detail := fmt.Sprintf("Put back %d misplaced episode file%s into %s", res.Moved+res.Recycled, plural(res.Moved+res.Recycled), plan.ShowFolder)
		if res.Recycled > 0 {
			detail += fmt.Sprintf(" (%d duplicate%s recycled)", res.Recycled, plural(res.Recycled))
		}
		detail += ": " + summarizeLines(lines, 12)
		if len(res.RemovedFolders) > 0 {
			detail += ". Removed empty folders: " + strings.Join(res.RemovedFolders, ", ")
		}
		c.series.AddEvent(ctx, seriesID, "files.repaired", detail)
		c.log.Info("series: misplaced files put back", "series", s.Title, "moved", res.Moved, "recycled", res.Recycled, "folder", plan.ShowFolder)
		c.libraryChanged("show", plan.ShowFolder)
	}
	if len(res.Skipped) > 0 {
		c.series.AddEvent(ctx, seriesID, "files.repair-skipped", renameSkippedEvent(res.Skipped))
	}
	return res, nil
}

// recycleMisplaced sends a misplaced copy (and its subtitles) to the recycle bin and points
// its episodes at the copy that stays. The bin must take it: with the bin off or failing,
// nothing is removed.
func (c *Coordinator) recycleMisplaced(ctx context.Context, s series.Series, it MisplacedItem) error {
	bin := c.bin()
	if _, err := bin.For(it.From); err != nil {
		if errors.Is(err, library.ErrRecycleDisabled) {
			return errors.New("the recycle bin is off, so nothing was removed")
		}
		return err
	}
	if err := library.CheckBin(bin, it.From); err != nil {
		return err
	}
	ei, err := os.Stat(it.Existing)
	if err != nil {
		return fmt.Errorf("the copy in the show's folder is gone: %w", err)
	}
	subs := library.PairedSidecars(it.From)
	if _, err := library.RemoveToBin(bin, it.From); err != nil {
		return err
	}
	for _, sub := range subs {
		if _, err := library.RemoveToBin(bin, sub); err != nil {
			c.log.Warn("series: a misplaced copy's subtitle stayed behind", "file", sub, "err", err)
		}
	}
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			if e.HasFile && e.FilePath == it.From {
				if err := c.series.MarkEpisodeImported(ctx, s.ID, e.SeasonNumber, e.EpisodeNumber, it.Existing, ei.Size()); err != nil {
					c.log.Warn("series: couldn't point an episode at the copy that stays", "episode", fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber), "err", err)
				}
			}
		}
	}
	return nil
}

// pruneLeftFolders removes the folders misplaced files left, and the folders above them,
// while they're empty — never a TV folder, nothing outside one, and nothing with anything
// left in it (another show's files, artwork, a .plexignore). Returns what it removed.
func (c *Coordinator) pruneLeftFolders(left map[string]bool) []string {
	removed := []string{}
	roots := c.series.TVRoots()
	dirs := make([]string, 0, len(left))
	for d := range left {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		for dir := filepath.Clean(d); ; dir = filepath.Dir(dir) {
			inside := false
			for _, r := range roots {
				if libfolders.StrictlyUnder(dir, r) {
					inside = true
					break
				}
			}
			if !inside {
				break
			}
			if err := os.Remove(dir); err != nil {
				break // not empty (or not ours to remove)
			}
			removed = append(removed, dir)
		}
	}
	return removed
}

// summarizeLines joins up to n lines, saying how many more there were.
func summarizeLines(lines []string, n int) string {
	if len(lines) <= n {
		return strings.Join(lines, "; ")
	}
	return strings.Join(lines[:n], "; ") + fmt.Sprintf("; and %d more", len(lines)-n)
}
