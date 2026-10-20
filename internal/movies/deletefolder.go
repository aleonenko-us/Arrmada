package movies

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
)

// This file decides what "delete the movie with its files" removes besides the tracked
// files and their subtitles (GH #59): the artwork and metadata a media server or a
// scraper left in the movie's own folder, and the folder itself once it is empty. The
// same plan feeds the delete dialog and the delete, so the dialog states exactly what
// will go.
//
// Leftovers go only when the folder is provably this movie's own (ownFolder) and every
// one of them is a known sidecar type (sidecarKind). Anything else — a folder that isn't
// the movie's, an unknown file, a link, another video anywhere inside, the recycle bin
// switched off — leaves every leftover where it is, and the folder with them: only the
// tracked files and their subtitles go, as they always did.

// purgeVideo is one tracked file a movie delete moves, with the subtitles that go with it.
type purgeVideo struct {
	versionID int64 // 0 = the default track
	label     string
	path      string
	size      int64
	subs      []string
}

// purgeFolder is one folder holding tracked files, and what happens to it.
type purgeFolder struct {
	dir     string
	extras  []string // leftover sidecars that go too (full paths, sorted)
	subdirs []string // extras subfolders removed once emptied (full paths)
	remove  bool     // the folder is empty once everything above has gone, so it goes
	kept    string   // why its leftovers stay ("" when they go or there are none)
}

// purgePlan is everything a movie delete with files moves or removes. DeletePlan reads
// it for the dialog and removeAllFiles carries it out, so the two can't disagree.
type purgePlan struct {
	videos  []purgeVideo // only tracked files that are on disk (Lstat, like the delete)
	folders []purgeFolder
}

// paths is every path the plan removes, in the order the delete removes them: videos
// and their subtitles, then each folder's leftovers, its extras subfolders and itself.
func (p purgePlan) paths() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ps ...string) {
		for _, x := range ps {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	for _, v := range p.videos {
		add(v.path)
		add(v.subs...)
	}
	for _, f := range p.folders {
		add(f.extras...)
		add(f.subdirs...)
		if f.remove {
			add(f.dir)
		}
	}
	return out
}

// purgePlanFor works out what deleting movie m with its files removes. files are its
// tracked files (default first), deduplicated.
func (s *Service) purgePlanFor(m Movie, files []movieFile, labels map[int64]string) purgePlan {
	var plan purgePlan
	tracked := map[string]bool{}
	for _, f := range files {
		tracked[filepath.Clean(f.path)] = true
	}
	planned := map[string]bool{} // every file the plan already moves
	dirs := map[string]bool{}
	for _, f := range files {
		fi, err := os.Lstat(f.path)
		if err != nil {
			continue // already gone from disk: nothing to move
		}
		v := purgeVideo{versionID: f.versionID, label: labels[f.versionID], path: f.path, size: fi.Size()}
		if fi.Mode()&fs.ModeSymlink != 0 {
			if ti, err := os.Stat(f.path); err == nil {
				v.size = ti.Size()
			}
		}
		// A same-name video in another container that stays (X.mp4 beside X.mkv) keeps
		// the subtitles: they pair with it too. One that goes as well doesn't.
		if !sharesBaseWithUntracked(f.path, tracked) {
			for _, sub := range library.Sidecars(f.path) {
				if !planned[filepath.Clean(sub)] {
					planned[filepath.Clean(sub)] = true
					v.subs = append(v.subs, sub)
				}
			}
		}
		planned[filepath.Clean(f.path)] = true
		plan.videos = append(plan.videos, v)
		dirs[filepath.Dir(f.path)] = true
	}
	ordered := make([]string, 0, len(dirs))
	for d := range dirs {
		ordered = append(ordered, d)
	}
	// Deepest first, so a version's folder inside another goes before its parent is looked at.
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) != len(ordered[j]) {
			return len(ordered[i]) > len(ordered[j])
		}
		return ordered[i] < ordered[j]
	})
	for _, d := range ordered {
		plan.folders = append(plan.folders, s.planFolder(m, d, tracked, planned))
	}
	return plan
}

// planFolder decides what happens to dir, a folder holding some of the movie's tracked
// files, once they and their subtitles (planned) have gone.
func (s *Service) planFolder(m Movie, dir string, tracked, planned map[string]bool) purgeFolder {
	f := purgeFolder{dir: dir}
	if !s.prunable(dir) {
		if fi, err := os.Lstat(dir); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			f.kept = "it's a link to another folder"
		} else if err == nil && fi.IsDir() {
			f.kept = "it's one of your Movies folders"
		} else {
			f.kept = "it couldn't be read"
		}
		return f
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		f.kept = "it couldn't be read"
		return f
	}
	leftovers := false
	for _, e := range entries {
		if !planned[filepath.Clean(filepath.Join(dir, e.Name()))] {
			leftovers = true
			break
		}
	}
	if !leftovers {
		f.remove = true // only the movie's own files and subtitles were in it
		return f
	}
	if reason := s.ownFolder(m, dir); reason != "" {
		f.kept = reason
		return f
	}
	if off, reason := s.binOff(); off {
		f.kept = reason
		return f
	}
	extras, subdirs, reason := leftoverSidecars(dir, tracked, planned)
	if reason != "" {
		f.kept = reason
		return f
	}
	f.extras, f.subdirs, f.remove = extras, subdirs, true
	return f
}

// binOff reports whether a file would be deleted for good rather than moved
// to a recycle bin. Leftovers are never deleted for good: with the bin off they stay.
// It asks without creating anything: the dialog's preview uses it too.
func (s *Service) binOff() (bool, string) {
	if library.BinOff(s.bin) {
		return true, "the recycle bin is off, so its other files stay"
	}
	return false, ""
}

// prunable reports whether dir may be removed once empty: it exists as a real folder (not
// a symlink — os.Remove unlinks a symlink however full the folder it points at is) and it
// isn't one of the Movies folders, spelled in any case.
func (s *Service) prunable(dir string) bool {
	fi, err := os.Lstat(dir)
	if err != nil || fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return false
	}
	for _, r := range s.allRoots() {
		if strings.EqualFold(filepath.Clean(r), filepath.Clean(dir)) {
			return false
		}
	}
	return true
}

// allRoots is every Movies folder the service knows of: the configured ones, the live
// default and the one it started with. Arrmada keeps no history of earlier ones.
func (s *Service) allRoots() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range append(append(s.libRoots(), s.libRoot()), s.root) {
		if r = strings.TrimSpace(r); r == "" {
			continue
		}
		if c := filepath.Clean(r); !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// ownFolder returns "" when dir is provably movie m's own folder, otherwise why not:
//   - it lies strictly inside a Movies folder, isn't one, and holds none;
//   - nothing from that Movies folder down to dir is a symlink;
//   - its name is the movie's: its title followed by its year, ignoring case and
//     punctuation ("Heat (1995)", "Heat.1995.1080p", "heat (1995) {tmdb-949}"). That is
//     what the default folder naming gives. A name without the year, or one the year
//     doesn't follow the title in ("Marvel", "Iron Man 2 (2010)" for Iron Man), is
//     not proof enough.
//
// What dir holds is checked separately (leftoverSidecars).
func (s *Service) ownFolder(m Movie, dir string) string {
	dir = filepath.Clean(dir)
	root := ""
	for _, r := range s.allRoots() {
		if strings.EqualFold(r, dir) {
			return "it's a library folder"
		}
		if libfolders.StrictlyUnder(r, dir) || strings.HasPrefix(strings.ToLower(r), strings.ToLower(dir)+string(filepath.Separator)) {
			return "a library folder is inside it"
		}
		if libfolders.StrictlyUnder(dir, r) && len(r) > len(root) {
			root = r
		}
	}
	if root == "" {
		return "it isn't inside a Movies folder"
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "it isn't inside a Movies folder"
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil || fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return "it's reached through a link"
		}
	}
	if !s.folderNamedFor(m, filepath.Base(dir)) {
		return "its name isn't the movie's"
	}
	return ""
}

// folderNamedFor reports whether a folder called name is movie m's by name: one of its
// titles, then its year.
func (s *Service) folderNamedFor(m Movie, name string) bool {
	if m.Year <= 0 {
		return false // the year is half of the check: without it, refuse
	}
	words := nameWords(name)
	year := strconv.Itoa(m.Year)
	for _, title := range []string{m.Title, m.DisplayTitle, m.TitleOverride} {
		t := nameWords(title)
		if len(t) == 0 || len(words) <= len(t) {
			continue
		}
		match := true
		for i := range t {
			if words[i] != t[i] {
				match = false
				break
			}
		}
		if match && words[len(t)] == year {
			return true
		}
	}
	return false
}

// nameWords splits a title or folder name into lower-case words of letters and digits.
// Apostrophes join ("Ocean's" = "oceans"); everything else separates.
func nameWords(s string) []string {
	s = strings.NewReplacer("'", "", "’", "", "`", "").Replace(strings.ToLower(s))
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// sharesBaseWithUntracked reports whether a video with video's base name in another
// container sits beside it and isn't one of the files being deleted.
func sharesBaseWithUntracked(video string, tracked map[string]bool) bool {
	dir := filepath.Dir(video)
	name := filepath.Base(video)
	base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == name || tracked[filepath.Clean(filepath.Join(dir, e.Name()))] {
			continue
		}
		ext := filepath.Ext(e.Name())
		if isAnyVideo(e.Name()) && strings.ToLower(strings.TrimSuffix(e.Name(), ext)) == base {
			return true
		}
	}
	return false
}

// maxLeftovers bounds the walk of a movie folder: one holding more than this many
// entries is not a plain movie folder, so its leftovers stay.
const maxLeftovers = 500

// leftoverSidecars lists what in dir isn't already planned (the tracked files and their
// subtitles) and returns it when every piece is a known sidecar: files of a known type
// directly in dir, and extras-style subfolders holding only such files. reason says why
// not otherwise; then nothing is returned. Links are never followed: a symlink anywhere
// is a reason. A video anywhere that isn't one of the movie's tracked files is a reason.
func leftoverSidecars(dir string, tracked, planned map[string]bool) (extras, subdirs []string, reason string) {
	seen := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			reason = "part of it couldn't be read"
			return fs.SkipAll
		}
		if p == dir {
			return nil
		}
		if seen++; seen > maxLeftovers {
			reason = "it holds too many files to check"
			return fs.SkipAll
		}
		clean := filepath.Clean(p)
		rel, _ := filepath.Rel(dir, p)
		depth := strings.Count(rel, string(filepath.Separator))
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			reason = "it holds a link (" + rel + ")"
			return fs.SkipAll
		case d.IsDir():
			if depth > 0 || !extrasDirs[strings.ToLower(d.Name())] {
				// Walked anyway when it's an extras folder; anything else is unknown, but a
				// video inside says more about why.
				if hasVideo(p, tracked) {
					reason = "another video is inside it (" + rel + ")"
				} else {
					reason = "it holds a folder Arrmada doesn't recognise (" + rel + ")"
				}
				return fs.SkipAll
			}
			subdirs = append(subdirs, p)
			return nil
		case !d.Type().IsRegular():
			reason = "it holds something that isn't a file (" + rel + ")"
			return fs.SkipAll
		case planned[clean]:
			return nil // the movie's own file or subtitle, already going
		case isAnyVideo(d.Name()):
			if tracked[clean] {
				return nil
			}
			reason = "another video is inside it (" + rel + ")"
			return fs.SkipAll
		case !sidecarKind(d.Name()):
			reason = "it holds a file Arrmada doesn't recognise (" + rel + ")"
			return fs.SkipAll
		}
		extras = append(extras, p)
		return nil
	})
	if err != nil && reason == "" {
		reason = "part of it couldn't be read"
	}
	if reason != "" {
		return nil, nil, reason
	}
	sort.Strings(extras)
	sort.Slice(subdirs, func(i, j int) bool { return len(subdirs[i]) > len(subdirs[j]) })
	return extras, subdirs, ""
}

// hasVideo reports whether any video other than a tracked file is anywhere under dir
// (links not followed), stopping at maxLeftovers entries (then it says yes: refuse).
func hasVideo(dir string, tracked map[string]bool) bool {
	found, seen := false, 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if seen++; err != nil || seen > maxLeftovers {
			found = true
			return fs.SkipAll
		}
		if !d.IsDir() && isAnyVideo(d.Name()) && !tracked[filepath.Clean(p)] {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// extrasDirs are the subfolder names media servers and scrapers keep a movie's extra
// artwork and clips in. One goes only when everything in it is a known sidecar file.
var extrasDirs = map[string]bool{
	"extras": true, "featurettes": true, "behind the scenes": true, "deleted scenes": true,
	"interviews": true, "scenes": true, "shorts": true, "trailers": true, "other": true,
	"extrafanart": true, "extrathumbs": true,
}

// sidecarExts are the leftover file types a movie folder is emptied of: metadata,
// artwork, subtitles, release notes and checksums.
var sidecarExts = map[string]bool{
	".nfo": true,
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".tbn": true,
	".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true, ".vtt": true, ".sup": true, ".smi": true,
	".txt": true, ".url": true, ".sfv": true, ".md5": true,
}

// sidecarNames are other known leftovers, by whole name: Plex/Kodi theme music and the
// files Windows and macOS drop in any folder they show.
var sidecarNames = map[string]bool{
	"theme.mp3": true, "theme.flac": true, "theme.m4a": true, "theme.ogg": true, "theme.wav": true,
	".ds_store": true, "thumbs.db": true, "desktop.ini": true,
}

// sidecarKind reports whether a leftover file named name is a known sidecar type.
func sidecarKind(name string) bool {
	lower := strings.ToLower(name)
	return sidecarNames[lower] || sidecarExts[filepath.Ext(lower)]
}

// anyVideoExts is wider than what imports take on purpose: here it only decides that a
// folder holds another video, and so is not emptied.
var anyVideoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".ts": true, ".m2ts": true, ".mts": true,
	".mov": true, ".wmv": true, ".webm": true, ".mpg": true, ".mpeg": true, ".flv": true, ".iso": true,
	".vob": true, ".divx": true, ".xvid": true, ".ogm": true, ".ogv": true, ".rm": true, ".rmvb": true,
	".3gp": true, ".asf": true, ".f4v": true, ".mxf": true, ".img": true, ".strm": true,
}

func isAnyVideo(name string) bool { return anyVideoExts[strings.ToLower(filepath.Ext(name))] }
