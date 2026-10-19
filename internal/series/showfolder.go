package series

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Which folder a show lives in.
//
// It used to be read off ONE episode file: two folders up when the file's folder was named
// "Season N", one up otherwise. Libraries whose season folders are called "S01", "Stagione
// 1", "Saison 1" or anything else got the season folder back as the show's folder, so the
// next import went to <TV folder>/S13/Season 12/… — a new folder every show with an "S13"
// then shared — and once one file was there, every later import followed it (#56).
//
// Now the show folder is the first folder below the TV folder that holds the file, which
// needs no guess about season folder names at all, and the show's folder is the one most
// of its files are in — never a folder named like a season, never a TV folder (or a folder
// named after one), never a folder other shows' files are in.

// SetSeasonFolderMatcher adds the configured season folder template to what counts as a
// season folder (library.Importer.IsSeasonFolderName), so a library Arrmada itself names
// "{title} Season {season}" is read right. Call it at startup, before anything runs.
func (s *Service) SetSeasonFolderMatcher(fn func(name string) bool) { s.seasonFolder = fn }

// seasonLike reports whether a folder name is a season folder (Specials included) —
// something inside a show's folder, which can never be the show's own folder. Extras
// folders don't count here: "Extras" is a show too.
func (s *Service) seasonLike(name string) bool {
	if _, ok := library.SeasonFolderNumber(name); ok {
		return true
	}
	return s.seasonFolder != nil && s.seasonFolder(name)
}

// insideShow reports whether a folder name is one a show keeps inside its folder: a
// season folder, Specials, or Extras. For the folder-name guess only.
func (s *Service) insideShow(name string) bool {
	return library.LooksLikeSeasonFolder(name) || s.seasonLike(name)
}

// ShowFolder is the show folder an episode file lives in, judged by folder names alone:
// one level up from a season folder ("Season 04", "S04", "Stagione 4", "Specials"), the
// file's own folder otherwise. It is the fallback for a file under none of the TV
// folders; inside one, Service.ShowFolderFor reads it from the TV folder instead.
func ShowFolder(episodePath string) string {
	parent := filepath.Dir(episodePath)
	if library.LooksLikeSeasonFolder(filepath.Base(parent)) {
		return filepath.Dir(parent)
	}
	return parent
}

// TVRoots is every TV folder now, the default first.
func (s *Service) TVRoots() []string { return s.libRoots() }

// rootOf is the TV folder holding path (the deepest, when one is inside another) and
// whether there is one.
func rootOf(roots []string, path string) (string, bool) {
	best := ""
	for _, r := range roots {
		if strings.TrimSpace(r) == "" || !libfolders.StrictlyUnder(path, r) {
			continue
		}
		if len(filepath.Clean(r)) > len(best) {
			best = filepath.Clean(r)
		}
	}
	return best, best != ""
}

// folderCandidates are the folders that may be the show folder of the episode file at
// path, best first:
//
//   - inside a TV folder, the first folder below it that holds the file — what the show's
//     folder is in every library laid out <TV folder>/<Show>/…;
//   - then the second folder below it, and the folder above the file's season folder (or
//     its own folder) when that's deeper still — for shows grouped one level down
//     (<TV folder>/TV Shows/<Show>/S01/…, a TV folder set one level too high, or
//     <TV folder>/Anime/<Show>/…), used only when the folder above turns out to hold
//     several shows;
//   - outside every TV folder, that folder-name guess alone.
//
// A file loose in a TV folder has none.
func (s *Service) folderCandidates(roots []string, path string) []string {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	guess := dir
	if s.insideShow(filepath.Base(dir)) {
		guess = filepath.Dir(dir)
	}
	root, ok := rootOf(roots, path)
	if !ok {
		if guess == "" || guess == "." || guess == filepath.Dir(guess) {
			return nil
		}
		return []string{guess}
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil // loose in the TV folder
	}
	parts := strings.Split(rel, string(filepath.Separator))
	first := filepath.Join(root, parts[0])
	out := []string{first}
	if len(parts) > 1 {
		out = append(out, filepath.Join(first, parts[1]))
	}
	if last := out[len(out)-1]; libfolders.StrictlyUnder(guess, last) {
		out = append(out, guess)
	}
	return out
}

// ShowFolderFor is the show folder the episode file at path is in: the first folder below
// the TV folder holding it, or the folder-name guess (ShowFolder) for a file under none.
// It says where the file IS — whether that's a sound folder for the show is
// ShowFolderOf's question. "" for a file loose in a TV folder.
func (s *Service) ShowFolderFor(path string) string {
	c := s.folderCandidates(s.libRoots(), path)
	if len(c) == 0 {
		if _, inRoot := rootOf(s.libRoots(), path); inRoot {
			return ""
		}
		return ShowFolder(path)
	}
	return c[0]
}

// ShowFolderInfo is where a show lives on disk, as far as its episode files tell.
type ShowFolderInfo struct {
	// Path is the show's folder ("" when it has no files, or none in a folder of its own).
	Path string `json:"path"`
	// Files is how many of the show's files are in Path; Total how many it has.
	Files int `json:"files"`
	Total int `json:"total"`
	// Unknown: the show has files, but every folder they're in is unsound — named like a
	// season, a TV folder, or holding other shows' files. Nothing may be imported for the
	// show until that is sorted out; Why says what was found, in plain words.
	Unknown bool   `json:"unknown"`
	Why     string `json:"why,omitempty"`
}

// Why a folder can't be a show's own.
const (
	unsoundSeason = "season" // named like a season folder
	unsoundRoot   = "root"   // a TV folder, or named after one
	unsoundShared = "shared" // other shows' files are in it
)

// folderJudge decides which folders can be a show's own. others lists up to a few OTHER
// series with files under a folder; names describes them for a message.
type folderJudge struct {
	svc    *Service
	roots  []string
	others func(dir string) []int64
	names  func(ids []int64) string
	cache  map[string]folderVerdict
}

// folderVerdict is a folder judged: why it can't be a show's own ("" when it can), the
// kind of reason (unsound*), and how many other shows' files are in it.
type folderVerdict struct {
	why, kind string
	others    int
}

func (j *folderJudge) judge(dir string) folderVerdict {
	if v, ok := j.cache[dir]; ok {
		return v
	}
	v := j.verdict(dir)
	if j.cache == nil {
		j.cache = map[string]folderVerdict{}
	}
	j.cache[dir] = v
	return v
}

func (j *folderJudge) verdict(dir string) folderVerdict {
	base := filepath.Base(dir)
	if j.svc.seasonLike(base) {
		return folderVerdict{why: fmt.Sprintf("%s is named like a season folder", dir), kind: unsoundSeason}
	}
	for _, r := range j.roots {
		if filepath.Clean(r) == filepath.Clean(dir) {
			return folderVerdict{why: fmt.Sprintf("%s is a TV folder itself", dir), kind: unsoundRoot}
		}
		if strings.EqualFold(filepath.Base(filepath.Clean(r)), base) {
			return folderVerdict{why: fmt.Sprintf("%s is named after the TV folder %s", dir, r), kind: unsoundRoot}
		}
	}
	if others := j.others(dir); len(others) > 0 {
		names := "other shows"
		if j.names != nil {
			names = j.names(others)
		}
		return folderVerdict{why: fmt.Sprintf("%s also holds episodes of %s", dir, names), kind: unsoundShared, others: len(others)}
	}
	return folderVerdict{}
}

// dbJudge judges folders for one show against the database.
func (s *Service) dbJudge(ctx context.Context, seriesID int64) *folderJudge {
	return &folderJudge{
		svc:    s,
		roots:  s.libRoots(),
		others: func(dir string) []int64 { return s.repo.OtherSeriesUnder(ctx, seriesID, dir, 3) },
		names:  func(ids []int64) string { return s.describeSeries(ctx, ids) },
	}
}

// describeSeries names a few shows for a message.
func (s *Service) describeSeries(ctx context.Context, ids []int64) string {
	var names []string
	for _, id := range ids {
		if sr, err := s.repo.Get(ctx, id); err == nil {
			names = append(names, fmt.Sprintf("%q", sr.Title))
		}
	}
	if len(names) == 0 {
		return "other shows"
	}
	if len(ids) >= 3 {
		return strings.Join(names, ", ") + " and possibly more"
	}
	return strings.Join(names, " and ")
}

// containerShows is how many other shows' files make a first-level folder a container of
// shows (a TV folder set one level too high) rather than one show stored twice.
const containerShows = 2

// homeOf is the sound folder the file at path belongs to ("" when none), the verdict on
// the deepest folder that was turned down (zero when none was), and whether the file is in
// a folder at all (false: loose in a TV folder).
//
// A deeper folder only stands in for one above it that holds several shows. One other
// show there is the same show stored twice ("Teen Titans" and "Teen Titans Go!"), and
// picking a folder inside it would be a guess.
func (j *folderJudge) homeOf(path string) (string, folderVerdict, bool) {
	cands := j.svc.folderCandidates(j.roots, path)
	var turnedDown folderVerdict // the deepest folder turned down: the most telling reason
	for _, dir := range cands {
		v := j.judge(dir)
		if v.why == "" {
			return dir, turnedDown, true
		}
		turnedDown = v
		if v.kind != unsoundShared || v.others < containerShows {
			break
		}
	}
	return "", turnedDown, len(cands) > 0
}

// showFolderFrom is ShowFolderOf for a show's files, judged by j.
func (s *Service) showFolderFrom(paths []string, j *folderJudge) ShowFolderInfo {
	info := ShowFolderInfo{Total: len(paths)}
	if len(paths) == 0 {
		return info
	}
	counts := map[string]int{}
	var whys []string
	placed := false // any file in a folder below a TV folder (or outside them)
	for _, p := range paths {
		home, first, inFolder := j.homeOf(p)
		placed = placed || inFolder
		if home != "" {
			counts[home]++
		} else if first.why != "" {
			whys = append(whys, first.why)
		}
	}
	if len(counts) == 0 {
		if !placed {
			return info // only loose files in a TV folder: a new folder from the scheme
		}
		info.Unknown = true
		info.Why = firstDistinct(whys)
		return info
	}
	dirs := make([]string, 0, len(counts))
	for d := range counts {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, k int) bool {
		if counts[dirs[i]] != counts[dirs[k]] {
			return counts[dirs[i]] > counts[dirs[k]]
		}
		if len(dirs[i]) != len(dirs[k]) {
			return len(dirs[i]) < len(dirs[k])
		}
		return dirs[i] < dirs[k]
	})
	info.Path, info.Files = dirs[0], counts[dirs[0]]
	return info
}

// ShowFolderOf works out the folder a show lives in from all of its episode files: the
// sound folder (see folderJudge) holding most of them. Files already misplaced by the old
// season-folder bug — in <TV folder>/S13/Season 12 — are outvoted by the show's real
// folder, and never chosen on their own.
func (s *Service) ShowFolderOf(ctx context.Context, seriesID int64) ShowFolderInfo {
	paths, err := s.repo.EpisodeFilePaths(ctx, seriesID)
	if err != nil {
		return ShowFolderInfo{Unknown: true, Why: "the show's files couldn't be read: " + err.Error()}
	}
	j := s.dbJudge(ctx, seriesID)
	return s.showFolderFrom(s.dropGoneMisplaced(paths, j), j)
}

// dropGoneMisplaced leaves out files recorded in a folder no show should own that are no
// longer there — moved back by hand before a rescan caught up — so they don't keep a show
// from its folder. Only those files are looked at on disk; every other path is kept as
// recorded, so a share that's down never changes where a show lives.
func (s *Service) dropGoneMisplaced(paths []string, j *folderJudge) []string {
	gone := s.fileGone
	if gone == nil {
		gone = func(p string) bool {
			_, err := os.Stat(p)
			return errors.Is(err, fs.ErrNotExist)
		}
	}
	out := paths[:0:0]
	for _, p := range paths {
		if home, _, inFolder := j.homeOf(p); home == "" && inFolder && gone(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// MisplacedFile is one of a show's episode files that is outside its show's folder, in a
// folder no show should own: one named like a season folder right under the TV folder
// (<TV>/S13/Season 12, what #56 made), a TV folder itself, or — when the show does have a
// folder of its own — a folder other shows' files are in.
type MisplacedFile struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// misplacedFrom lists the misplaced files among a show's files (see MisplacedFile).
func (s *Service) misplacedFrom(info ShowFolderInfo, paths []string, j *folderJudge) []MisplacedFile {
	var out []MisplacedFile
	for _, p := range paths {
		if info.Path != "" && (libfolders.StrictlyUnder(p, info.Path)) {
			continue
		}
		home, first, inFolder := j.homeOf(p)
		if home != "" || !inFolder || first.why == "" {
			continue // in a sound folder (a second one is the owner's business), or loose
		}
		if first.kind == unsoundShared && info.Path == "" {
			continue // two shows in one ordinary folder, and nowhere better to put either
		}
		out = append(out, MisplacedFile{Path: p, Why: first.why})
	}
	return out
}

// MisplacedFiles is where the show lives and its episode files that are elsewhere, in a
// folder no show should own (MisplacedFile).
func (s *Service) MisplacedFiles(ctx context.Context, seriesID int64) (ShowFolderInfo, []MisplacedFile, error) {
	paths, err := s.repo.EpisodeFilePaths(ctx, seriesID)
	if err != nil {
		return ShowFolderInfo{}, nil, err
	}
	j := s.dbJudge(ctx, seriesID)
	paths = s.dropGoneMisplaced(paths, j)
	info := s.showFolderFrom(paths, j)
	return info, s.misplacedFrom(info, paths, j), nil
}

// MisplacedShow is a show with episode files outside its folder, for the health check.
type MisplacedShow struct {
	SeriesID int64  `json:"series_id"`
	Title    string `json:"title"`
	Files    int    `json:"files"`
	// NoFolder: none of its files are in a folder of its own.
	NoFolder bool `json:"no_folder"`
}

// MisplacedShows lists every show with misplaced episode files (MisplacedFiles), worked
// out from the database alone in one pass — it never looks at the disk, so it can't wake
// a sleeping array.
func (s *Service) MisplacedShows(ctx context.Context) ([]MisplacedShow, error) {
	rows, err := s.repo.AllEpisodeFilePaths(ctx)
	if err != nil {
		return nil, err
	}
	roots := s.libRoots()
	// Every folder above each file, up to its TV folder, with the shows that have files in
	// it: "other shows under this folder" without a query per folder.
	under := map[string]map[int64]bool{}
	byShow := map[int64][]string{}
	for _, r := range rows {
		byShow[r.SeriesID] = append(byShow[r.SeriesID], r.Path)
		root, inRoot := rootOf(roots, r.Path)
		for d := filepath.Dir(filepath.Clean(r.Path)); ; d = filepath.Dir(d) {
			if inRoot && !libfolders.StrictlyUnder(d, root) {
				break
			}
			if d == filepath.Dir(d) {
				break
			}
			if under[d] == nil {
				under[d] = map[int64]bool{}
			}
			under[d][r.SeriesID] = true
		}
	}
	var out []MisplacedShow
	for id, paths := range byShow {
		id := id
		j := &folderJudge{svc: s, roots: roots, others: func(dir string) []int64 {
			var ids []int64
			for o := range under[filepath.Clean(dir)] {
				if o != id {
					ids = append(ids, o)
					if len(ids) == 3 {
						break
					}
				}
			}
			return ids
		}}
		info := s.showFolderFrom(paths, j)
		if m := s.misplacedFrom(info, paths, j); len(m) > 0 {
			out = append(out, MisplacedShow{SeriesID: id, Files: len(m), NoFolder: info.Path == ""})
		}
	}
	for i := range out {
		if sr, err := s.repo.Get(ctx, out[i].SeriesID); err == nil {
			out[i].Title = sr.Title
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Files != out[b].Files {
			return out[a].Files > out[b].Files
		}
		return out[a].Title < out[b].Title
	})
	return out, nil
}

func firstDistinct(whys []string) string {
	seen := map[string]bool{}
	var out []string
	for _, w := range whys {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
		if len(out) == 2 {
			break
		}
	}
	return strings.Join(out, "; ")
}

// ShowFolderError is a show whose episodes can't be imported because there is no sound
// folder to put them in. The download goes to Review with Error() as its reason.
type ShowFolderError struct {
	Title string
	Why   string
}

// ErrNoShowFolder is what every ShowFolderError is (errors.Is), for handlers to answer
// with a conflict rather than a server fault.
var ErrNoShowFolder = errors.New("no safe folder for the show")

// Unwrap makes a ShowFolderError an ErrNoShowFolder.
func (e *ShowFolderError) Unwrap() error { return ErrNoShowFolder }

func (e *ShowFolderError) Error() string {
	return fmt.Sprintf("Can't tell which folder %s lives in (%s), so nothing was imported. Move the show's files into its own folder (the show's page offers to put misplaced files back), then import again.", e.Title, e.Why)
}

// ImportDir is the folder the show's own folder sits in — the TV folder, normally — for
// the importer to place its episodes under (with ExistingFolderName as the show folder).
// A show whose folder is deeper than one level below its TV folder (a TV folder set one
// level too high) gets that folder's parent, so its episodes join it rather than a new
// folder beside it. "" is the importer's default TV folder.
func (s *Service) ImportDir(ctx context.Context, sr Series) string {
	return s.ImportDirOf(ctx, sr, s.ShowFolderOf(ctx, sr.ID))
}

// ImportDirOf is ImportDir for a show whose folder was already worked out (info).
func (s *Service) ImportDirOf(ctx context.Context, sr Series, info ShowFolderInfo) string {
	if p := info.Path; p != "" {
		if _, ok := rootOf(s.libRoots(), p); ok {
			return filepath.Dir(p)
		}
	}
	return s.RootFor(ctx, sr)
}

// UnsafeImportFolder says why dir must not take sr's episodes ("" when it may): it's
// named like a season folder, it's a TV folder (or named after one), or another show's
// files are in it. The last check before any episode file is placed.
func (s *Service) UnsafeImportFolder(ctx context.Context, sr Series, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return s.dbJudge(ctx, sr.ID).judge(filepath.Clean(dir)).why
}
