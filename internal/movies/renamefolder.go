package movies

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/library"
)

// What moves with a renamed track besides its file. Two kinds, both listed in the preview
// (RenameItem.Extras), and applying moves only what the preview listed and a fresh look
// at the disk still allows:
//
//   - the subtitles paired with the file (library.PairedSidecars), renamed to the new
//     name, unless another film's recorded file pairs with them as well (pairsWithOther)
//     or a same-name video in another container stays beside them;
//   - when the film leaves its folder, that folder's metadata and artwork — but only
//     when the folder is provably the movie's own, by the same rules a movie delete uses
//     (deletefolder.go): named for the movie, not a library or downloads folder, no link
//     on the way to it or inside it, no other video anywhere in it, no other film's
//     record in it (links resolved), no link from elsewhere into it, and every leftover
//     a known sidecar named generically or for this movie. One named for the old file
//     ("Old Name.nfo", "Old Name-poster.jpg") takes the new file's name.
//
// Anything else stays exactly where it is (RenameItem.Kept says why). The old folder is
// removed only when it ends up empty, only with os.Remove, and never when it is a link,
// a library or downloads folder; nothing above it is ever touched.

// Kinds of RenameExtra.
const (
	ExtraSubtitle = "subtitle" // a subtitle paired with the track's file
	ExtraFolder   = "folder"   // a leftover of the movie's own folder
)

// RenameExtra is one file that moves along with a renamed track.
type RenameExtra struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

// othersFunc returns every file recorded for the library's other films (on disk or not),
// read at most once per plan.
type othersFunc func() ([]string, error)

func memoOthers(fn func() ([]string, error)) othersFunc {
	var (
		done bool
		out  []string
		err  error
	)
	return func() ([]string, error) {
		if !done {
			out, err = fn()
			done = true
		}
		return out, err
	}
}

// othersOfMovie reads the other films' records for movie id from the database.
func (s *Service) othersOfMovie(ctx context.Context, id int64) othersFunc {
	return memoOthers(func() ([]string, error) { return s.repo.OtherRecordedPaths(ctx, id) })
}

// libraryOthers reads every recorded file once and hands each movie the others'.
func (s *Service) libraryOthers(ctx context.Context) func(id int64) othersFunc {
	var rows []recordedPath
	all := memoOthers(func() ([]string, error) {
		var err error
		rows, err = s.repo.RecordedPaths(ctx)
		return nil, err
	})
	return func(id int64) othersFunc {
		return memoOthers(func() ([]string, error) {
			if _, err := all(); err != nil {
				return nil, err
			}
			out := make([]string, 0, len(rows))
			for _, r := range rows {
				if r.movieID != id {
					out = append(out, r.path)
				}
			}
			return out, nil
		})
	}
}

// renameMove is one track file leaving a folder: where it was and where it goes.
type renameMove struct{ from, to string }

// planExtras fills in what moves with each clean item of one movie's plan: its paired
// subtitles, and the leftovers of every folder the movie leaves (on the first item
// leaving that folder), or why they stay.
func (s *Service) planExtras(m Movie, versions []Version, items []RenameItem, others othersFunc) {
	var moving []int
	for i := range items {
		if items[i].Status == RenameOK {
			moving = append(moving, i)
		}
	}
	if len(moving) == 0 {
		return
	}
	oth, oerr := others()
	claimed := map[string]bool{} // every name something in this plan moves to
	planned := map[string]bool{} // every file this plan moves (clean paths)
	for _, i := range moving {
		claimed[filepath.Clean(items[i].Proposed)] = true
		planned[filepath.Clean(items[i].Current)] = true
	}
	for _, i := range moving {
		it := &items[i]
		it.Extras = s.planSubs(it.Current, it.Proposed, ownOtherFiles(versions, it.Current), oth, oerr, claimed, planned)
	}
	byDir := map[string][]int{}
	var dirs []string
	for _, i := range moving {
		od, nd := filepath.Dir(items[i].Current), filepath.Dir(items[i].Proposed)
		if od == nd {
			continue
		}
		if _, ok := byDir[od]; !ok {
			dirs = append(dirs, od)
		}
		byDir[od] = append(byDir[od], i)
	}
	sort.Strings(dirs)
	for _, od := range dirs {
		var moves []renameMove
		for _, i := range byDir[od] {
			moves = append(moves, renameMove{items[i].Current, items[i].Proposed})
		}
		staying := stayingFiles(versions, moves)
		extras, kept := s.planLeave(m, od, moves, staying, oth, oerr, planned, claimed)
		first := &items[byDir[od][0]]
		first.Extras = append(first.Extras, extras...)
		first.Kept = kept
	}
}

// ownOtherFiles is every file the movie's tracks record except path.
func ownOtherFiles(versions []Version, path string) []string {
	var out []string
	for _, v := range versions {
		if v.HasFile && v.FilePath != "" && v.FilePath != path {
			out = append(out, v.FilePath)
		}
	}
	return out
}

// stayingFiles is every file the movie's tracks record that none of moves takes away.
func stayingFiles(versions []Version, moves []renameMove) []string {
	going := map[string]bool{}
	for _, mv := range moves {
		going[mv.from] = true
	}
	var out []string
	for _, v := range versions {
		if v.HasFile && v.FilePath != "" && !going[v.FilePath] {
			out = append(out, v.FilePath)
		}
	}
	return out
}

// planSubs lists the subtitles paired with video cur that follow it to prop, renamed to
// match. None follow when the other films' records can't be read, or when a video with
// cur's name in another container stays beside it (they pair with that one too); one that
// another recorded file pairs with as well (pairsWithOther, the delete's guard) stays, and
// so does one whose new name is taken.
func (s *Service) planSubs(cur, prop string, own, others []string, oerr error, claimed, planned map[string]bool) []RenameExtra {
	if oerr != nil || sharesBaseWithUntracked(cur, map[string]bool{filepath.Clean(cur): true}) {
		return nil
	}
	pairs := append(append([]string{}, others...), own...)
	oldBase := strings.TrimSuffix(filepath.Base(cur), filepath.Ext(cur))
	newBase := strings.TrimSuffix(prop, filepath.Ext(prop))
	var out []RenameExtra
	for _, sub := range library.PairedSidecars(cur) {
		if pairsWithOther(sub, cur, pairs) {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(sub), filepath.Ext(sub))
		if len(stem) < len(oldBase) || !strings.EqualFold(stem[:len(oldBase)], oldBase) {
			continue // a case fold that changed the length: leave it rather than guess
		}
		to := newBase + stem[len(oldBase):] + filepath.Ext(sub)
		if !free(to, claimed) {
			continue
		}
		claimed[filepath.Clean(to)] = true
		planned[filepath.Clean(sub)] = true
		out = append(out, RenameExtra{Kind: ExtraSubtitle, From: sub, To: to})
	}
	return out
}

// free reports whether nothing in the plan and nothing on disk has the name to.
func free(to string, claimed map[string]bool) bool {
	if claimed[filepath.Clean(to)] {
		return false
	}
	_, err := os.Lstat(to)
	return errors.Is(err, fs.ErrNotExist)
}

// planLeave decides what of folder od's other files follow the movie's tracks (moves) to
// their new folder: every one of them when od is provably the movie's own, none otherwise
// (kept says why, "" when there's nothing else in od). staying are the movie's recorded
// files that don't move; planned are the files the plan already moves.
func (s *Service) planLeave(m Movie, od string, moves []renameMove, staying, others []string, oerr error, planned, claimed map[string]bool) ([]RenameExtra, string) {
	entries, err := os.ReadDir(od)
	if err != nil {
		return nil, ""
	}
	leftovers := false
	for _, e := range entries {
		if !planned[filepath.Clean(filepath.Join(od, e.Name()))] {
			leftovers = true
			break
		}
	}
	if !leftovers {
		return nil, ""
	}
	nd := filepath.Dir(moves[0].to)
	for _, mv := range moves[1:] {
		if filepath.Dir(mv.to) != nd {
			return nil, "the movie's files go to different folders"
		}
	}
	if !s.prunable(od) {
		if fi, err := os.Lstat(od); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return nil, "it's a link to another folder"
		}
		return nil, "it's one of your library or downloads folders"
	}
	if strings.EqualFold(od, nd) {
		return nil, "the new folder's name differs from it only in case"
	}
	if insideFold(nd, od) || insideFold(od, nd) {
		return nil, "the new folder is inside it, or it is inside the new folder"
	}
	// The cheap checks first: most folders that aren't the movie's fail on their name or
	// on what they hold, before every other film's record is looked at.
	if reason := s.ownFolder(m, od); reason != "" {
		return nil, reason
	}
	if o := recordedInto(od, staying); o != "" {
		return nil, "another of this movie's files stays in it (" + relName(od, o) + ")"
	}
	tracked := map[string]bool{}
	var renames []baseRename
	var bases []string
	for _, mv := range moves {
		tracked[filepath.Clean(mv.from)] = true
		ob := strings.TrimSuffix(filepath.Base(mv.from), filepath.Ext(mv.from))
		renames = append(renames, baseRename{ob, strings.TrimSuffix(filepath.Base(mv.to), filepath.Ext(mv.to))})
		bases = append(bases, strings.ToLower(ob))
	}
	files, _, reason := leftoverSidecars(od, tracked, planned, leftoverNamedFor(m, bases))
	if reason != "" {
		return nil, reason
	}
	if oerr != nil {
		return nil, "the library's other films couldn't be checked"
	}
	if o := recordedInto(od, others); o != "" {
		return nil, "another film in your library has a file recorded in it (" + relName(od, o) + ")"
	}
	if link := s.aliasInto(od); link != "" {
		return nil, "another folder links into it (" + filepath.Base(link) + ")"
	}
	var out []RenameExtra
	for _, x := range files {
		rel, err := filepath.Rel(od, x)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if !strings.ContainsRune(rel, filepath.Separator) {
			rel = renamedFor(rel, renames)
		}
		to := filepath.Join(nd, rel)
		if !free(to, claimed) {
			continue // the new folder already has one: both stay where they are
		}
		claimed[filepath.Clean(to)] = true
		out = append(out, RenameExtra{Kind: ExtraFolder, From: x, To: to})
	}
	return out, ""
}

// baseRename is a moved file's name without extension, before and after.
type baseRename struct{ old, new string }

// renamedFor gives a leftover named for a moved file ("Old Name.nfo", "Old Name-poster.jpg",
// "Old Name.en.srt") that file's new name, keeping what follows; the longest old name that
// matches wins. Any other name is kept.
func renamedFor(name string, renames []baseRename) string {
	best := -1
	for i, r := range renames {
		if r.old == "" || len(name) < len(r.old) || !strings.EqualFold(name[:len(r.old)], r.old) {
			continue
		}
		if len(name) > len(r.old) && !strings.ContainsRune(" .-_[(", rune(name[len(r.old)])) {
			continue
		}
		if best < 0 || len(r.old) > len(renames[best].old) {
			best = i
		}
	}
	if best < 0 {
		return name
	}
	return renames[best].new + name[len(renames[best].old):]
}

// insideFold reports whether p lies strictly inside dir, ignoring case.
func insideFold(p, dir string) bool {
	return strings.HasPrefix(strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(dir))+string(filepath.Separator))
}

// relName is o relative to dir, for a reason shown to the person.
func relName(dir, o string) string {
	rel, err := filepath.Rel(dir, o)
	if err != nil {
		return filepath.Base(o)
	}
	return filepath.ToSlash(rel)
}

// extraKey names one extra move, for matching a fresh plan against the preview.
func extraKey(x RenameExtra) string { return filepath.Clean(x.From) + "\x00" + filepath.Clean(x.To) }

// moveSubs moves a track's paired subtitles after its file moved: the ones the fresh plan
// lists that the preview listed too (allowed). It returns what moved, so a failed record
// can put exactly those back.
func (s *Service) moveSubs(extras []RenameExtra, allowed map[string]bool) []RenameExtra {
	var moved []RenameExtra
	for _, x := range extras {
		if x.Kind != ExtraSubtitle || !allowed[extraKey(x)] {
			continue
		}
		if err := s.imp.Move(x.From, x.To); err != nil {
			s.log.Info("movie rename: a subtitle stayed where it was", "file", x.From, "err", err)
			continue
		}
		moved = append(moved, x)
	}
	return moved
}

// leaveFolder finishes a rename that moved tracks (moves) out of folder od. The folder is
// looked at again now that they've gone — another film's record, a new video or unknown
// file, a link: every leftover stays — and only leftovers both the preview (allowed) and
// this fresh look list move. The folder and its emptied extras subfolders are removed
// once empty, never when one is a link or a library or downloads folder.
func (s *Service) leaveFolder(ctx context.Context, m Movie, versions []Version, od string, moves []renameMove, allowed map[string]bool) {
	others, oerr := s.repo.OtherRecordedPaths(ctx, m.ID)
	extras, kept := s.planLeave(m, od, moves, stayingFiles(versions, moves), others, oerr, map[string]bool{}, map[string]bool{})
	if kept != "" {
		s.log.Info("movie rename: the old folder's other files stay", "dir", od, "why", kept)
	}
	var subdirs []string
	for _, x := range extras {
		if !allowed[extraKey(x)] {
			continue
		}
		if fi, err := os.Lstat(x.From); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if err := s.imp.Move(x.From, x.To); err != nil {
			s.log.Info("movie rename: left a folder file behind", "file", x.From, "err", err)
			continue
		}
		if d := filepath.Dir(x.From); d != od {
			subdirs = append(subdirs, d)
		}
	}
	sort.Slice(subdirs, func(i, j int) bool { return len(subdirs[i]) > len(subdirs[j]) })
	for _, d := range subdirs {
		s.pruneEmpty(d)
	}
	s.pruneEmpty(od)
}

// pruneEmpty removes dir when it is an empty real folder (not a link) that isn't a
// library or downloads folder. os.Remove only ever removes an empty folder.
func (s *Service) pruneEmpty(dir string) {
	if !s.prunable(dir) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	if err := os.Remove(dir); err == nil {
		s.log.Info("removed empty folder after rename", "dir", dir)
	}
}
