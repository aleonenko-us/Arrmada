package movies

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
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
// Leftovers go only when the folder is provably this movie's own (ownFolder), no other
// film in the library has a file recorded in it (present or not), and every leftover is
// a known sidecar type (sidecarKind) named generically or for this movie
// (leftoverNamedFor). Anything else — a folder that isn't the movie's, an unknown file or
// a file named for something else, a link, another video anywhere inside, the recycle
// bin switched off — leaves every leftover where it is, and the folder with them: only
// the tracked files and their subtitles go, as they always did.
//
// The preview is binding: the delete only moves the leftovers the caller confirms it was
// shown (DeleteOptions.Leftovers), and only those a fresh plan still allows. A delete
// without that list (the API without it, the bulk delete) moves no leftovers.

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
	root    string   // the Movies folder it is in (set when it has leftovers)
	stamps  map[string]fileStamp
}

// fileStamp is what a leftover looked like when the plan was made.
type fileStamp struct {
	size  int64
	mtime time.Time
}

// purgePlan is everything a movie delete with files moves or removes. DeletePlan reads
// it for the dialog and removeAllFiles carries it out, so the two can't disagree.
type purgePlan struct {
	videos  []purgeVideo // only tracked files that are on disk (Lstat, like the delete)
	folders []purgeFolder
	pc      purgeCtx // what the plan was made from, for the delete's re-checks
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

// keepOnly drops every leftover that isn't in confirmed (the paths the preview listed).
// The folder itself is still removed only if it ends up empty.
func (p *purgePlan) keepOnly(confirmed []string) {
	ok := map[string]bool{}
	for _, c := range confirmed {
		ok[filepath.Clean(c)] = true
	}
	for i := range p.folders {
		var kept []string
		for _, x := range p.folders[i].extras {
			if ok[filepath.Clean(x)] {
				kept = append(kept, x)
			}
		}
		p.folders[i].extras = kept
	}
}

// purgeCtx is what the plan knows while it looks at the movie's folders.
type purgeCtx struct {
	m         Movie
	tracked   map[string]bool // the movie's recorded files (clean paths)
	planned   map[string]bool // every file the plan already moves (clean paths)
	others    []string        // every file recorded for other films, on disk or not
	othersErr error           // they couldn't be read: no leftover goes
	bases     []string        // the movie's recorded files' names without extension, lower case
}

// purgePlanFor works out what deleting movie m with its files removes. files are its
// tracked files (default first), deduplicated.
func (s *Service) purgePlanFor(ctx context.Context, m Movie, files []movieFile, labels map[int64]string) purgePlan {
	var plan purgePlan
	pc := purgeCtx{m: m, tracked: map[string]bool{}, planned: map[string]bool{}}
	pc.others, pc.othersErr = s.repo.OtherRecordedPaths(ctx, m.ID)
	for _, f := range files {
		pc.tracked[filepath.Clean(f.path)] = true
		name := filepath.Base(f.path)
		pc.bases = append(pc.bases, strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))))
	}
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
		if !sharesBaseWithUntracked(f.path, pc.tracked) {
			for _, sub := range library.Sidecars(f.path) {
				// Another film's recorded file pairs with it as well (or better): even
				// when that file is missing from disk, the subtitle may be its.
				if pc.othersErr != nil || pairsWithOther(sub, f.path, pc.others) {
					continue
				}
				if !pc.planned[filepath.Clean(sub)] {
					pc.planned[filepath.Clean(sub)] = true
					v.subs = append(v.subs, sub)
				}
			}
		}
		pc.planned[filepath.Clean(f.path)] = true
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
	// One probe for the whole plan: each folder and record is looked at once.
	probe := newLinkProbe(pc.others)
	for _, d := range ordered {
		plan.folders = append(plan.folders, s.planFolder(pc, probe, d))
	}
	plan.pc = pc
	return plan
}

// pairsWithOther reports whether subtitle sub pairs with another film's recorded file in
// its folder whose name is at least as long as video's: "Movie.Directors.Cut.en.srt"
// belongs to a recorded "Movie.Directors.Cut.mkv" rather than to "Movie.mkv", and one
// both name exactly is never taken. Case is ignored; the other file may be missing.
func pairsWithOther(sub, video string, others []string) bool {
	dir := filepath.Dir(sub)
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(sub), filepath.Ext(sub)))
	vb := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	for _, o := range others {
		if !strings.EqualFold(filepath.Dir(filepath.Clean(o)), dir) {
			continue
		}
		ob := strings.ToLower(strings.TrimSuffix(filepath.Base(o), filepath.Ext(o)))
		if len(ob) < len(vb) {
			continue
		}
		if stem == ob || strings.HasPrefix(stem, ob+".") {
			return true
		}
	}
	return false
}

// recordedInto returns another film's recorded file that is dir itself or lies in or
// under it ("" when none). Paths are compared as written, ignoring case, and again with
// every link resolved (pathguard.Resolve: the deepest part that exists, links followed,
// the missing rest joined back), so a record made through an alias of the folder
// ("movies/HeatAlias/…" where HeatAlias links to "movies/Heat (1995)") still counts, even
// with its file missing. A record or folder that can't be resolved counts too: refuse
// rather than guess. A pass that asks more than once shares a linkProbe instead.
func recordedInto(dir string, others []string) string {
	return newLinkProbe(others).recordedInto(dir)
}

// aliasInto returns a symlink that points at dir or into it ("" when none is found).
// It looks where an alias of a movie folder would sit — directly in each Movies folder
// and beside dir — without walking the whole library. A link that can't be resolved
// counts: refuse rather than guess. p is the pass's probe (what it has read is reused).
func (s *Service) aliasInto(p *linkProbe, dir string) string {
	return p.aliasInto(dir, s.allRoots())
}

// planFolder decides what happens to dir, a folder holding some of the movie's tracked
// files, once they and their subtitles (planned) have gone.
// probe is the plan's (newLinkProbe over pc.others).
func (s *Service) planFolder(pc purgeCtx, probe *linkProbe, dir string) purgeFolder {
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
		if !pc.planned[filepath.Clean(filepath.Join(dir, e.Name()))] {
			leftovers = true
			break
		}
	}
	if !leftovers {
		f.remove = true // only the movie's own files and subtitles were in it
		return f
	}
	if pc.othersErr != nil {
		f.kept = "the library's other films couldn't be checked"
		return f
	}
	if o := probe.recordedInto(dir); o != "" {
		rel, err := filepath.Rel(dir, o)
		if err != nil {
			rel = filepath.Base(o)
		}
		f.kept = "another film in your library has a file recorded in it (" + filepath.ToSlash(rel) + ")"
		return f
	}
	if link := s.aliasInto(probe, dir); link != "" {
		f.kept = "another folder links into it (" + filepath.Base(link) + ")"
		return f
	}
	if reason := s.ownFolder(pc.m, dir); reason != "" {
		f.kept = reason
		return f
	}
	if off, reason := s.binOff(); off {
		f.kept = reason
		return f
	}
	extras, subdirs, reason := leftoverSidecars(dir, pc.tracked, pc.planned, leftoverNamedFor(pc.m, pc.bases))
	if reason != "" {
		f.kept = reason
		return f
	}
	f.extras, f.subdirs, f.remove = extras, subdirs, true
	f.root = s.movieRootOf(dir)
	f.stamps = map[string]fileStamp{}
	for _, x := range extras {
		if fi, err := os.Lstat(x); err == nil {
			f.stamps[x] = fileStamp{fi.Size(), fi.ModTime()}
		}
	}
	return f
}

// recheckFolder looks at a folder again right before its leftovers move, after the
// movie's own files went: another film's record that turned up, or a new video or
// unknown file (an import landing mid-delete), keeps every leftover. "" means go on.
func (s *Service) recheckFolder(ctx context.Context, pc purgeCtx, f purgeFolder) string {
	others, err := s.repo.OtherRecordedPaths(ctx, pc.m.ID)
	if err != nil {
		return "the library's other films couldn't be checked"
	}
	// A fresh probe: nothing the plan saw stands in for a look now.
	probe := newLinkProbe(others)
	if o := probe.recordedInto(f.dir); o != "" {
		return "another film has a file recorded in it now"
	}
	if link := s.aliasInto(probe, f.dir); link != "" {
		return "another folder links into it now"
	}
	if off, reason := s.binOff(); off {
		return reason
	}
	if _, _, reason := leftoverSidecars(f.dir, pc.tracked, pc.planned, leftoverNamedFor(pc.m, pc.bases)); reason != "" {
		return reason
	}
	return ""
}

// leftoverChanged returns why leftover p mustn't move now ("" when it may): every folder
// from the Movies folder down to it must still be a real folder (no link swapped in), and
// p must be the plain file the plan saw, at the same size and modification time.
func (s *Service) leftoverChanged(f purgeFolder, p string) string {
	if f.root == "" {
		return "its Movies folder is unknown"
	}
	rel, err := filepath.Rel(f.root, filepath.Dir(p))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "it isn't inside its Movies folder"
	}
	cur := f.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil || fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return "a folder on its way is gone or a link now"
		}
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return "it's gone or no longer a plain file"
	}
	st, ok := f.stamps[p]
	if !ok || st.size != fi.Size() || !st.mtime.Equal(fi.ModTime()) {
		return "it changed since the preview"
	}
	return ""
}

// movieRootOf is the deepest Movies folder dir lies strictly inside ("" when none).
func (s *Service) movieRootOf(dir string) string {
	root := ""
	for _, r := range s.allRoots() {
		if libfolders.StrictlyUnder(dir, r) && len(r) > len(root) {
			root = r
		}
	}
	return root
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
	for _, r := range s.protectedDirs() {
		if strings.EqualFold(filepath.Clean(r), filepath.Clean(dir)) {
			return false
		}
	}
	return true
}

// protectedDirs is every folder a delete never removes or empties: the Movies folders
// and, when the app has set them (SetProtectedDirs), every other library folder and
// every downloads folder.
func (s *Service) protectedDirs() []string {
	out := s.allRoots()
	if s.protected != nil {
		for _, p := range s.protected() {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, filepath.Clean(p))
			}
		}
	}
	return out
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
	for _, r := range s.protectedDirs() {
		if strings.EqualFold(r, dir) {
			return "it's a library or downloads folder"
		}
		if strings.HasPrefix(strings.ToLower(r), strings.ToLower(dir)+string(filepath.Separator)) {
			return "a library or downloads folder is inside it"
		}
	}
	root := s.movieRootOf(dir)
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
func (s *Service) folderNamedFor(m Movie, name string) bool { return titleYearPrefix(m, name) }

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
// subtitles) and returns it when every piece is a known sidecar named for this movie or
// generically (ours): such files directly in dir, artwork in extrafanart/extrathumbs,
// and such files in other extras-style subfolders. reason says why not otherwise; then
// nothing is returned. Links are never followed: a symlink anywhere is a reason. A video
// anywhere that isn't one of the movie's tracked files is a reason.
func leftoverSidecars(dir string, tracked, planned map[string]bool, ours func(name string) bool) (extras, subdirs []string, reason string) {
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
		case depth == 1 && artDirs[strings.ToLower(filepath.Base(filepath.Dir(p)))] && imageExts[strings.ToLower(filepath.Ext(d.Name()))]:
			// extrafanart/1.jpg: artwork in a folder kept for this one movie's art
		case !ours(d.Name()):
			reason = "it holds a file named for something else (" + rel + ")"
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

// leftoverNamedFor returns whether a leftover file's name is generic ("poster.jpg",
// "movie.nfo", "theme.mp3") or names this movie: it starts with one of its recorded
// files' names ("Heat (1995)-poster.jpg", "Heat (1995).en.srt"), or with its title then
// its year ("Heat.1995.nfo"). A file named for anything else ("Ronin (1998).nfo",
// "Iron Man 2 (2010).en.srt" in Iron Man's folder) is not this movie's to recycle.
func leftoverNamedFor(m Movie, bases []string) func(name string) bool {
	return func(name string) bool {
		lower := strings.ToLower(name)
		if sidecarNames[lower] {
			return true
		}
		stem := strings.TrimSuffix(lower, filepath.Ext(lower))
		if genericStems[strings.TrimRight(stem, "0123456789")] {
			return true
		}
		sub := subtitleLeftover[filepath.Ext(lower)]
		for _, b := range bases {
			if b == "" || !strings.HasPrefix(stem, b) {
				continue
			}
			if len(stem) == len(b) || strings.ContainsRune(" .-_[(", rune(stem[len(b)])) && ownSuffix(nameWords(stem[len(b):]), sub) {
				return true
			}
		}
		return titleYearExactly(m, stem, sub)
	}
}

// ownSuffix reports whether what follows a movie's own name in a leftover's name still
// names the movie's own file: nothing, one artwork kind ("-poster", "-fanart"), or for a
// subtitle only language and flag tags (".en", ".eng.forced"). Anything else ("- Directors
// Cut") may name another film, so it isn't the movie's.
func ownSuffix(words []string, subtitle bool) bool {
	if len(words) == 0 {
		return true
	}
	if len(words) == 1 && artSuffixes[words[0]] {
		return true
	}
	if !subtitle {
		return false
	}
	for _, w := range words {
		if !subtitleTags[w] && !isLangCode(w) {
			return false
		}
	}
	return true
}

// titleYearExactly reports whether name is one of m's titles and its year and nothing
// more but an own suffix (ownSuffix): "Heat (1995)", "Heat.1995-poster", "Heat (1995).en".
func titleYearExactly(m Movie, name string, subtitle bool) bool {
	if m.Year <= 0 {
		return false
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
		if match && words[len(t)] == year && ownSuffix(words[len(t)+1:], subtitle) {
			return true
		}
	}
	return false
}

// artSuffixes are the artwork and metadata kinds Kodi, Plex and Jellyfin put after a
// movie's name ("Heat (1995)-poster.jpg").
var artSuffixes = map[string]bool{
	"poster": true, "fanart": true, "banner": true, "logo": true, "clearlogo": true,
	"clearart": true, "disc": true, "discart": true, "landscape": true, "thumb": true,
	"backdrop": true, "keyart": true, "cover": true,
}

// subtitleTags are the flags a subtitle's name carries after its language.
var subtitleTags = map[string]bool{"forced": true, "sdh": true, "cc": true, "hi": true, "default": true}

var subtitleLeftover = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true, ".vtt": true, ".sup": true, ".smi": true}

// isLangCode reports whether w reads as a language code: two or three letters
// ("en", "eng", "pt") or a region-tagged one already split into words ("pt", "br").
func isLangCode(w string) bool {
	if len(w) < 2 || len(w) > 3 {
		return false
	}
	for _, r := range w {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// titleYearPrefix reports whether name's words start with one of m's titles and then
// its year.
func titleYearPrefix(m Movie, name string) bool {
	if m.Year <= 0 {
		return false
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

// genericStems are artwork and metadata names that belong to whatever folder they're in
// (Kodi, Plex, Jellyfin, Emby), with any number after them ("fanart1").
var genericStems = map[string]bool{
	"poster": true, "folder": true, "fanart": true, "banner": true, "logo": true,
	"clearlogo": true, "clearart": true, "disc": true, "discart": true, "landscape": true,
	"backdrop": true, "thumb": true, "cover": true, "keyart": true, "movie": true,
}

// artDirs hold only artwork for the one movie whose folder they're in.
var artDirs = map[string]bool{"extrafanart": true, "extrathumbs": true}

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".tbn": true}

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
