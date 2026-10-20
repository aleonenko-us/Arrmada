package series

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/parser"
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

// filePlace is where an episode file sits, read from the folders around it.
type filePlace struct {
	root string // the TV folder holding it ("" when it's under none)
	// dir is the deepest folder holding the file that isn't a season, disc or release
	// folder: the show's folder in every ordinary layout. "" for a file loose in a TV
	// folder.
	dir string
	// structural: dir was reached by stepping out of a season, disc or release folder, so
	// it is the show's folder, however deep it is (/tv/Anime/Frieren/Season 1/…).
	structural bool
}

// reDiscFolder is a disc folder inside a season folder ("Disc 1", "CD2").
var reDiscFolder = regexp.MustCompile(`(?i)^(?:disc|disk|cd|dvd|bd)[ ._-]*\d{1,2}$`)

// releaseLike reports whether a folder is a download's own folder (a season or episode
// tag, a stated resolution) or a disc folder: something inside a show's folder.
func releaseLike(name string) bool {
	if reDiscFolder.MatchString(name) {
		return true
	}
	r := parser.Parse(name)
	return r.Season > 0 || len(r.Episodes) > 0 || r.StatedResolution() != parser.ResUnknown
}

// placeOf reads where the episode file at path sits. Inside a TV folder it steps out of up
// to two season, disc or release folders (Show/Season 1/Show.S01E01.1080p/…), never into
// the TV folder itself. Outside every TV folder it's the folder-name guess (ShowFolder).
func (s *Service) placeOf(roots []string, path string) filePlace {
	path = filepath.Clean(path)
	root, ok := rootOf(roots, path)
	if !ok {
		guess := filepath.Dir(path)
		if s.insideShow(filepath.Base(guess)) {
			guess = filepath.Dir(guess)
		}
		if guess == "" || guess == "." || guess == filepath.Dir(guess) {
			return filePlace{}
		}
		return filePlace{dir: guess}
	}
	dir := filepath.Dir(path)
	if !libfolders.StrictlyUnder(dir, root) {
		return filePlace{root: root} // loose in the TV folder
	}
	structural, season := false, false
	for step := 0; step < 2; step++ {
		up := filepath.Dir(dir)
		if !libfolders.StrictlyUnder(up, root) {
			break // never step out into the TV folder itself
		}
		name := filepath.Base(dir)
		inSeason := s.insideShow(name)
		if season && inSeason {
			break // one season folder only: <TV>/S13/Season 12 is S13's, not <TV>'s
		}
		if !inSeason && !releaseLike(name) {
			break
		}
		dir, structural, season = up, true, season || inSeason
	}
	return filePlace{root: root, dir: dir, structural: structural}
}

// firstLevel reports whether dir sits right inside root.
func firstLevel(dir, root string) bool {
	return root != "" && filepath.Clean(filepath.Dir(dir)) == filepath.Clean(root)
}

// ShowFolderFor is the folder the episode file at path is in, for a show: the deepest
// folder holding it that isn't a season, disc or release folder, or the folder-name guess
// (ShowFolder) for a file under no TV folder. It says where the file IS — whether that's a
// sound folder for the show is ShowFolderOf's question, and it never names a folder above
// the one holding the files. "" for a file loose in a TV folder.
func (s *Service) ShowFolderFor(path string) string {
	return s.placeOf(s.libRoots(), path).dir
}

// ShowFoldersOf answers, for each episode file of show seriesID, the folder of the show it
// is in: the folder ShowFolderOf would count it towards (a show stored flat in a category
// folder is found by its name), else where it is (ShowFolderFor). For Move to folder,
// which moves a show's folders whole.
func (s *Service) ShowFoldersOf(ctx context.Context, seriesID int64) func(path string) string {
	j := s.dbJudge(ctx, seriesID)
	return func(path string) string {
		if home, _, _ := j.homeOf(path); home != "" {
			return home
		}
		return s.ShowFolderFor(path)
	}
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
	// sharedMost: Unknown because most of the show's files are in a folder other shows'
	// files are in too. None of them is "misplaced" then: which show owns the folder is the
	// person's call.
	sharedMost bool
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
	// titles are the show's names (parser.TitleKey), for picking its folder by name when
	// nothing else tells: a show stored flat inside a category folder (/tv/Anime/Show/…).
	titles map[string]bool
	cache  map[string]folderVerdict
}

// folderVerdict is a folder judged: why it can't be a show's own ("" when it can), the
// kind of reason (unsound*), and how many other shows' files are in it.
type folderVerdict struct {
	dir, why, kind string
	others         int
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
		return folderVerdict{dir: dir, why: fmt.Sprintf("%s is named like a season folder", dir), kind: unsoundSeason}
	}
	for _, r := range j.roots {
		if filepath.Clean(r) == filepath.Clean(dir) {
			return folderVerdict{dir: dir, why: fmt.Sprintf("%s is a TV folder itself", dir), kind: unsoundRoot}
		}
	}
	// A folder named after the TV folder it's in (<TV>/TV/…) is what the old flat-layout
	// bug made. Only that TV folder's name counts: /tv/Anime is a show folder in /tv even
	// when another TV folder is /media/Anime.
	if root, ok := rootOf(j.roots, dir); ok && strings.EqualFold(filepath.Base(root), base) {
		return folderVerdict{dir: dir, why: fmt.Sprintf("%s is named after the TV folder %s", dir, root), kind: unsoundRoot}
	}
	if others := j.others(dir); len(others) > 0 {
		names := "other shows"
		if j.names != nil {
			names = j.names(others)
		}
		return folderVerdict{dir: dir, why: fmt.Sprintf("%s also holds episodes of %s", dir, names), kind: unsoundShared, others: len(others)}
	}
	return folderVerdict{dir: dir}
}

// named reports whether a folder is named after the show (a bracketed year or tag aside),
// or after what the file in it calls the show (fileKey, a parser.TitleKey). A release
// folder ("Frieren.S01.1080p.WEB") doesn't count, however its title reads.
func (j *folderJudge) named(dir, fileKey string) bool {
	k := parser.TitleKey(filepath.Base(dir))
	if k == "" {
		return false
	}
	return j.titles[k] || (fileKey != "" && k == fileKey)
}

// dbJudge judges folders for one show against the database.
func (s *Service) dbJudge(ctx context.Context, seriesID int64) *folderJudge {
	titles := map[string]bool{}
	if sr, err := s.repo.Get(ctx, seriesID); err == nil {
		for _, t := range []string{sr.Title, sr.DisplayTitle, sr.TitleOverride} {
			if k := parser.TitleKey(t); k != "" {
				titles[k] = true
			}
		}
	}
	return &folderJudge{
		svc:    s,
		roots:  s.libRoots(),
		others: func(dir string) []int64 { return s.repo.OtherSeriesUnder(ctx, seriesID, dir, 3) },
		names:  func(ids []int64) string { return s.describeSeries(ctx, ids) },
		titles: titles,
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

// unsoundUnsure: a folder deeper than the first below the TV folder, holding the show's
// files directly (or in folders with names Arrmada doesn't know as seasons), that can't be
// told apart from a show folder with odd season folders: /tv/Anime/Sousou no Frieren/ is
// Frieren's folder in a category, /tv/Avatar/Book 1 Water/ a season folder in Avatar's.
const unsoundUnsure = "unsure"

// homeOf is the sound folder the file at path belongs to ("" when none), the verdict on
// the folder that was turned down (zero when none was), and whether the file is in a
// folder at all (false: loose in a TV folder).
//
// It never names a folder above the one holding the file unless that is proven to be the
// show's: the file's own folder is the show's when it's right inside the TV folder, when
// it was reached by stepping out of a season, disc or release folder, or when it's named
// after the show (or after what the file calls it). Anything else is unsure and turned
// down — a guess at the category folder put a show's next season into /tv/Anime/Season 1.
func (j *folderJudge) homeOf(path string) (string, folderVerdict, bool) {
	pl := j.svc.placeOf(j.roots, path)
	if pl.dir == "" {
		return "", folderVerdict{}, false
	}
	proven := pl.root == "" || pl.structural || firstLevel(pl.dir, pl.root)
	dir := pl.dir
	if !proven {
		// A folder named after the show: the file's own, or one above it below the TV
		// folder ("Avatar (2005)/Book 1 Water").
		fileKey := parser.TitleKey(parser.Parse(filepath.Base(path)).Title)
		for d := pl.dir; libfolders.StrictlyUnder(d, pl.root); d = filepath.Dir(d) {
			if j.named(d, fileKey) {
				dir, proven = d, true
				break
			}
		}
	}
	if !proven {
		return "", folderVerdict{dir: pl.dir, kind: unsoundUnsure,
			why: fmt.Sprintf("%s could be the show's folder or a folder inside it", pl.dir)}, true
	}
	if v := j.judge(dir); v.why != "" {
		return "", v, true
	}
	return dir, folderVerdict{}, true
}

// showFolderFrom is ShowFolderOf for a show's files, judged by j.
func (s *Service) showFolderFrom(paths []string, j *folderJudge) ShowFolderInfo {
	info := ShowFolderInfo{Total: len(paths)}
	if len(paths) == 0 {
		return info
	}
	counts := map[string]int{}
	// Files in folders that are probably the show's but can't be used: other shows' files
	// are there too, or it can't be told whether the folder is the show's (unsure).
	held := map[string]int{}
	heldWhy := map[string]string{}
	unsure := map[string][]string{} // unsure folders by the folder above them
	var whys []string
	placed := false // any file in a folder below a TV folder (or outside them)
	for _, p := range paths {
		home, first, inFolder := j.homeOf(p)
		placed = placed || inFolder
		switch {
		case home != "":
			counts[home]++
		case first.kind == unsoundUnsure:
			up := filepath.Dir(first.dir)
			unsure[up] = append(unsure[up], first.dir)
			held[first.dir]++
			heldWhy[first.dir] = first.why
		case first.why != "":
			whys = append(whys, first.why)
			if first.kind == unsoundShared {
				held[first.dir]++
				heldWhy[first.dir] = first.why
			}
		}
	}
	// A show's files spread over two or more such folders side by side are in one show's
	// season folders ("Book 1 Water", "Book 2 Earth"), never in two shows: the folder
	// above them is the show's.
	for up, dirs := range unsure {
		distinct := map[string]bool{}
		for _, d := range dirs {
			distinct[d] = true
		}
		if len(distinct) < 2 {
			continue
		}
		if _, inRoot := rootOf(j.roots, up); !inRoot {
			continue // the TV folder itself, or outside them all
		}
		if v := j.judge(up); v.why != "" {
			continue
		}
		counts[up] += len(dirs)
		for d := range distinct {
			delete(held, d)
		}
	}
	for d := range held {
		whys = append(whys, heldWhy[d])
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
	// A stray folder holding a few of the show's files never wins over a folder holding
	// more of them that another show's files are in too, or that can't be told to be the
	// show's: that one is probably the show's real folder, and the person decides.
	for dir, n := range held {
		if n > counts[dirs[0]] {
			info.Unknown, info.sharedMost, info.Why = true, true, heldWhy[dir]
			return info
		}
	}
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
	if info.sharedMost {
		return nil // most of its files are in a shared folder: nothing to put "back"
	}
	var out []MisplacedFile
	for _, p := range paths {
		if info.Path != "" && (libfolders.StrictlyUnder(p, info.Path)) {
			continue
		}
		home, first, inFolder := j.homeOf(p)
		if home != "" || !inFolder || first.why == "" {
			continue // in a sound folder (a second one is the owner's business), or loose
		}
		if first.kind == unsoundUnsure {
			continue // may well be the show's own folder: not "misplaced"
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
