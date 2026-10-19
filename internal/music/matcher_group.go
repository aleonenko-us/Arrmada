package music

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Grouping (MUS-31): which files of a download are one album. One group per download
// folder, whatever subfolders it has ("CD1", "Disc 2", "Bonus Tracks"); a disc folder's
// number is kept as the files' disc. The download splits only when its tags clearly name
// different albums. (A discography is grouped by its album folders before it gets here.)

// MatchGroup is a set of files matched together, as one album.
type MatchGroup struct {
	Dir   string // the folder the group's files share
	Files []MatchFile
}

// reDiscDir is a folder named for one disc: "CD2", "Disc 1", "disk03", "CD 2 - Title".
var reDiscDir = regexp.MustCompile(`(?i)^(?:cd|dis[ck])[\s._-]*0*(\d{1,2})\b`)

// DiscOfFolder is the disc a folder name says its files are on (0: it doesn't say).
func DiscOfFolder(name string) int {
	m := reDiscDir.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// commonShare is Lidarr's rule for a tag being common to a folder: the most common value
// covers at least 75% of the files that have one (so at most 25% differ).
const commonShare = 0.75

// GroupFiles groups a download's audio files into albums.
func GroupFiles(files []MatchFile) []MatchGroup {
	all := make([]MatchFile, 0, len(files))
	for _, f := range files {
		if d := DiscOfFolder(filepath.Base(filepath.Dir(f.Path))); d > 0 && f.FolderDisc == 0 {
			f.FolderDisc = d
		}
		all = append(all, f)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	var out []MatchGroup
	for _, g := range splitByTags(all) {
		if len(g) > 0 {
			out = append(out, MatchGroup{Dir: commonDir(g), Files: g})
		}
	}
	return out
}

// commonDir is the deepest folder every file is in (a disc folder's files: its parent).
func commonDir(files []MatchFile) string {
	dir := filepath.Dir(files[0].Path)
	for _, f := range files[1:] {
		for !within(f.Path, dir) {
			parent := filepath.Dir(dir)
			if parent == dir {
				return dir
			}
			dir = parent
		}
	}
	if DiscOfFolder(filepath.Base(dir)) > 0 {
		dir = filepath.Dir(dir)
	}
	return dir
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// splitByTags splits one folder's files when their album tags clearly disagree: the most
// common album (with its album artist) covers less than 75% of the tagged files. Files
// without an album tag join the biggest group.
func splitByTags(files []MatchFile) [][]MatchFile {
	key := func(f MatchFile) string {
		if f.Tags.Album == "" {
			return ""
		}
		return NormKey(f.Tags.AlbumArtist) + "\x00" + NormKey(f.Tags.Album)
	}
	counts := map[string]int{}
	var order []string
	tagged := 0
	for _, f := range files {
		k := key(f)
		if k == "" {
			continue
		}
		tagged++
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	if len(order) < 2 {
		return [][]MatchFile{files}
	}
	top := order[0]
	for _, k := range order {
		if counts[k] > counts[top] {
			top = k
		}
	}
	if float64(counts[top]) >= commonShare*float64(tagged) {
		return [][]MatchFile{files}
	}
	groups := map[string][]MatchFile{}
	for _, f := range files {
		k := key(f)
		if k == "" {
			k = top
		}
		groups[k] = append(groups[k], f)
	}
	out := make([][]MatchFile, 0, len(order))
	for _, k := range order {
		out = append(out, groups[k])
	}
	return out
}
