package stage

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Extras (MUS-36): what else a download brings for an album besides its audio — artwork
// and scans, a booklet PDF, lyrics, the rip log, cue sheets, playlists — moves with the
// album. Extras are never tagged or changed, so they are hardlinked into the album folder
// (copied when the drives differ) even while the download seeds, and nothing already in
// the album folder is ever replaced: an extra whose name is taken is skipped.

// DefaultExtras is the allowlist (the music_extras setting) Arrmada starts with.
const DefaultExtras = ".jpg .jpeg .png .webp .gif .pdf .lrc .txt .log .cue .accurip .m3u .m3u8 .nfo"

// maxExtraBytes skips anything bigger than a booklet could reasonably be: a stray video
// or disc image named like an extra isn't one.
const maxExtraBytes = 512 << 20

// ParseExtras reads an allowlist: extensions separated by spaces or commas, the dot
// optional, any case.
func ParseExtras(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' || r == '\t' }) {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" || f == "." {
			continue
		}
		if !strings.HasPrefix(f, ".") {
			f = "." + f
		}
		out[f] = true
	}
	return out
}

// Extra is one extra file: where it is in the download, and where it goes.
type Extra struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// PlanExtras lists the extras in base (the folder the album's audio was found in, read
// recursively, hidden folders skipped) and where each goes under albumDir, keeping its
// place relative to base. placed maps each placed audio file's source path, without its
// extension, to its library path without extension: a .lrc beside a track with the same
// name follows the track's new name. A top-level cover.* or folder.* image is kept as
// folder.<ext>, since cover.jpg is the house cover's name.
func PlanExtras(base, albumDir string, allow map[string]bool, placed map[string]string) []Extra {
	var out []Extra
	if len(allow) == 0 {
		return nil
	}
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !allow[ext] || strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			return nil
		}
		if fi, err := d.Info(); err != nil || fi.Size() > maxExtraBytes {
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		to := filepath.Join(albumDir, rel)
		stem := strings.TrimSuffix(p, filepath.Ext(p))
		switch {
		case ext == ".lrc" && placed[stem] != "":
			to = placed[stem] + filepath.Ext(p)
		case filepath.Dir(rel) == "." && isImage(ext):
			switch strings.ToLower(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))) {
			case "cover", "folder":
				to = filepath.Join(albumDir, "folder"+ext)
			}
		}
		out = append(out, Extra{From: p, To: to})
		return nil
	})
	// cover.* before folder.*: the download's own front wins folder.jpg, then the rest.
	sort.SliceStable(out, func(a, b int) bool { return out[a].From < out[b].From })
	return out
}

func isImage(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return true
	}
	return false
}

// PlaceExtras hardlinks (or copies) each extra into place, never over anything: an extra
// whose target is taken is skipped and logged, unless it's the very same content. It
// returns the paths placed and how many were skipped.
func PlaceExtras(extras []Extra, log *slog.Logger) (placed []string, skipped int) {
	if log == nil {
		log = slog.Default()
	}
	for _, e := range extras {
		if _, err := os.Lstat(e.To); err == nil {
			if !sameBytes(e.From, e.To) {
				log.Info("music: an extra's name is taken in the album folder — kept the file there, skipped the extra",
					"extra", filepath.Base(e.From), "target", e.To)
			}
			skipped++
			continue
		}
		if err := linkOrCopyNew(e.From, e.To); err != nil {
			if !errors.Is(err, ErrTargetTaken) {
				log.Warn("music: couldn't bring an extra along", "extra", e.From, "err", err)
			}
			skipped++
			continue
		}
		placed = append(placed, e.To)
	}
	return placed, skipped
}

// sameBytes reports whether two small files hold the same content (or are one file).
func sameBytes(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr != nil || berr != nil {
		return false
	}
	if os.SameFile(ai, bi) {
		return true
	}
	if ai.Size() != bi.Size() || ai.Size() > 64<<20 {
		return false
	}
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}
