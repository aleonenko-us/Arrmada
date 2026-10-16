package arrimport

import (
	"path/filepath"
	"strings"
)

// Folder is one of Arrmada's folders for the library being matched.
type Folder struct {
	ID   int64
	Path string
}

// Mapping is a root folder and the Arrmada folder it corresponds to.
type Mapping struct {
	Root
	// FolderID is the Arrmada folder it already is (nil: none yet).
	FolderID *int64 `json:"folder_id"`
	// MatchedBy says how: "path" (the same folder) or "name" (a folder with the same last
	// name — the same share, mounted at another place in each container).
	MatchedBy string `json:"matched_by,omitempty"`
	// SuggestedPath is where the folder looks to be inside Arrmada's container, for adding
	// it ("" when Arrmada can't see it at the same path; the person picks).
	SuggestedPath string `json:"suggested_path"`
}

// Map pairs each root folder with an Arrmada folder: the same path first, else the one
// folder with the same last name. A root nobody matches is offered as a new folder at its
// own path when Arrmada can see that path (exists), else left for the person to pick. An
// Arrmada folder is matched to one root at most.
func Map(roots []Root, folders []Folder, exists func(string) bool) []Mapping {
	used := map[int64]bool{}
	out := make([]Mapping, len(roots))
	for i, r := range roots {
		out[i].Root = r
		for _, f := range folders {
			if !used[f.ID] && strings.TrimSpace(f.Path) != "" && filepath.Clean(f.Path) == filepath.Clean(r.Path) {
				id := f.ID
				out[i].FolderID, out[i].MatchedBy = &id, "path"
				used[f.ID] = true
				break
			}
		}
	}
	for i, r := range roots {
		if out[i].FolderID != nil {
			continue
		}
		name := strings.ToLower(BaseName(r.Path))
		var hit *Folder
		n := 0
		for j, f := range folders {
			if !used[f.ID] && name != "" && strings.ToLower(filepath.Base(filepath.Clean(f.Path))) == name {
				hit = &folders[j]
				n++
			}
		}
		if n == 1 {
			id := hit.ID
			out[i].FolderID, out[i].MatchedBy = &id, "name"
			used[hit.ID] = true
			continue
		}
		if exists != nil && strings.HasPrefix(r.Path, "/") && exists(r.Path) {
			out[i].SuggestedPath = r.Path
		}
	}
	return out
}
