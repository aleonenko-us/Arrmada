package audioserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
)

// AudioFile is one audio file of an item, with what ffprobe found in it.
type AudioFile struct {
	Index    int
	Ino      string
	Path     string
	Name     string
	Ext      string
	Size     int64
	MTimeMs  int64
	Duration float64
	Bitrate  int
	Codec    string
	Title    string
	Narrator string // from the file's tags (narratorFromTags); "" when none says
	Chapters []Chapter
}

// metaVersion is what this probe stores per file. Version 2 added the narrator; a
// version 1 row still serves its length and chapters, and is read again once.
const metaVersion = 2

// Chapter is a chapter marker.
type Chapter struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

type prober struct {
	db      *sql.DB
	ffprobe string
	cache   filesCache
	// stat and walk are the only ways the prober touches the disk (nil = os.Stat and
	// library.FindBookFiles), so tests can count them: a warm-up with nothing new to read
	// must not wake the array's disks.
	stat func(string) (os.FileInfo, error)
	walk func(string) []library.FoundFile
}

func (p *prober) statPath(path string) (os.FileInfo, error) {
	if p.stat != nil {
		return p.stat(path)
	}
	return os.Stat(path)
}

func (p *prober) walkDir(path string) []library.FoundFile {
	if p.walk != nil {
		return p.walk(path)
	}
	return library.FindBookFiles(path)
}

// known is what the database already holds about the audio files of one item (its
// folder, or its single file). Answering it touches no disk.
type known struct {
	Files    int     // probed files with a length
	Seconds  float64 // their total length
	Bytes    int64   // their total size, as of the probe
	Chapters int     // embedded chapter markers across them
	Stale    int     // files probed before narrators were read (metaVersion), due a re-read
	Narrator string  // the narrator the first file (by path) names, if any does
}

// underRange is the primary-key range holding root and everything below it: root+sep up
// to root+(sep+1). It goes through the index, and never matches a sibling whose name
// merely starts the same ("Title (Full cast)" next to "Title").
func underRange(root string) (clean, from, to string) {
	clean = filepath.Clean(root)
	return clean, clean + string(filepath.Separator), clean + string(rune(filepath.Separator+1))
}

// knownUnder sums the probed files of an item from the database alone.
func (p *prober) knownUnder(ctx context.Context, root string) known {
	var k known
	if root == "" {
		return k
	}
	clean, from, to := underRange(root)
	_ = p.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(duration), 0), COALESCE(SUM(size), 0),
		        COALESCE(SUM(CASE WHEN json_valid(chapters) THEN json_array_length(chapters) ELSE 0 END), 0),
		        COALESCE(SUM(meta_version < ?), 0),
		        COALESCE((SELECT narrator FROM audio_file_meta
		                   WHERE narrator != '' AND duration > 0 AND (path = ? OR (path >= ? AND path < ?))
		                   ORDER BY path LIMIT 1), '')
		   FROM audio_file_meta WHERE duration > 0 AND (path = ? OR (path >= ? AND path < ?))`,
		metaVersion, clean, from, to, clean, from, to).Scan(&k.Files, &k.Seconds, &k.Bytes, &k.Chapters, &k.Stale, &k.Narrator)
	return k
}

// knownPaths lists every file with stored details at root or below it.
func (p *prober) knownPaths(ctx context.Context, root string) ([]string, error) {
	clean, from, to := underRange(root)
	rows, err := p.db.QueryContext(ctx,
		`SELECT path FROM audio_file_meta WHERE path = ? OR (path >= ? AND path < ?)`, clean, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, rows.Err()
}

// forget drops the stored details of files that are gone, returning how many went.
func (p *prober) forget(ctx context.Context, paths []string) int {
	n := 0
	for _, path := range paths {
		if res, err := p.db.ExecContext(ctx, `DELETE FROM audio_file_meta WHERE path = ?`, path); err == nil {
			if c, _ := res.RowsAffected(); c > 0 {
				n++
			}
		}
	}
	return n
}

// files lists an item's audio files in play order, probed (cached per file).
func (p *prober) files(ctx context.Context, path string, probe bool) ([]AudioFile, error) {
	return p.filesFor(ctx, path, "", probe)
}

// filesFor is files for an item whose book is by author: a file probed now doesn't take
// an album_artist naming the author as its narrator (narratorFromTags).
func (p *prober) filesFor(ctx context.Context, path, author string, probe bool) ([]AudioFile, error) {
	now := time.Now()
	if !probe {
		if files, ok := p.cache.get(path, now); ok {
			return files, nil
		}
	}
	gen := p.cache.generation()
	out, err := p.listFor(ctx, path, author, probe)
	if err == nil {
		p.cache.put(path, out, now, gen)
	}
	return out, err
}

func (p *prober) list(ctx context.Context, path string, probe bool) ([]AudioFile, error) {
	return p.listFor(ctx, path, "", probe)
}

func (p *prober) listFor(ctx context.Context, path, author string, probe bool) ([]AudioFile, error) {
	st, err := p.statPath(path)
	if err != nil {
		return nil, err
	}
	var paths []string
	if st.IsDir() {
		for _, f := range p.walkDir(path) {
			if library.IsAudiobookFile(f.Path) {
				paths = append(paths, f.Path)
			}
		}
		sort.SliceStable(paths, func(i, j int) bool { return naturalLess(relTo(path, paths[i]), relTo(path, paths[j])) })
	} else {
		paths = []string{path}
	}
	if len(paths) == 0 {
		return nil, errors.New("no audio files")
	}
	base := path
	if !st.IsDir() {
		base = filepath.Dir(path)
	}
	out := make([]AudioFile, 0, len(paths))
	for i, fp := range paths {
		fi, err := p.statPath(fp)
		if err != nil {
			continue
		}
		af := AudioFile{Index: i + 1, Ino: inoFor(relTo(base, fp)), Path: fp, Name: filepath.Base(fp),
			Ext: strings.ToLower(filepath.Ext(fp)), Size: fi.Size(), MTimeMs: fi.ModTime().UnixMilli()}
		if probe {
			p.meta(ctx, &af, author)
		} else {
			p.cached(ctx, &af)
		}
		out = append(out, af)
	}
	return out, nil
}

// cached fills in probed details only if they are already known.
func (p *prober) cached(ctx context.Context, af *AudioFile) bool {
	ok, _ := p.lookup(ctx, af)
	return ok
}

// lookup fills in stored details when they match the file on disk (size and time);
// current is false for a row an older probe wrote (before metaVersion).
func (p *prober) lookup(ctx context.Context, af *AudioFile) (ok, current bool) {
	var size, mtime int64
	var chapters string
	var version int
	err := p.db.QueryRowContext(ctx,
		`SELECT size, mtime, duration, bitrate, codec, title, narrator, chapters, meta_version FROM audio_file_meta WHERE path = ?`, af.Path).
		Scan(&size, &mtime, &af.Duration, &af.Bitrate, &af.Codec, &af.Title, &af.Narrator, &chapters, &version)
	if err != nil || size != af.Size || mtime != af.MTimeMs {
		af.Duration, af.Bitrate, af.Codec, af.Title, af.Narrator = 0, 0, "", "", ""
		return false, false
	}
	_ = json.Unmarshal([]byte(chapters), &af.Chapters)
	return true, version >= metaVersion
}

// meta fills in probed details, probing and caching when not known yet, or known only
// from an older probe.
func (p *prober) meta(ctx context.Context, af *AudioFile, author string) {
	ok, current := p.lookup(ctx, af)
	if ok && current {
		return
	}
	// An older row is read again once: if ffprobe can't read the file this time, the row
	// (if any) is marked current anyway, so the warm-up doesn't try it every half hour.
	settle := func() {
		_, _ = p.db.ExecContext(ctx, `UPDATE audio_file_meta SET meta_version = ? WHERE path = ?`, metaVersion, af.Path)
	}
	pctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, p.ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_chapters", "-show_streams", "-select_streams", "a:0", af.Path).Output()
	if err != nil {
		if ctx.Err() == nil {
			settle()
		}
		return
	}
	if !parseProbe(out, af, author) {
		settle()
		return
	}
	ch, _ := json.Marshal(af.Chapters)
	_, _ = p.db.ExecContext(ctx,
		`INSERT INTO audio_file_meta (path, size, mtime, duration, bitrate, codec, title, narrator, chapters, meta_version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, duration = excluded.duration,
		   bitrate = excluded.bitrate, codec = excluded.codec, title = excluded.title, narrator = excluded.narrator,
		   chapters = excluded.chapters, meta_version = excluded.meta_version`,
		af.Path, af.Size, af.MTimeMs, af.Duration, af.Bitrate, af.Codec, af.Title, af.Narrator, string(ch), metaVersion)
}

// bookChapters builds the chapter list for a whole item: embedded chapters of a single
// file, otherwise one chapter per file.
func bookChapters(files []AudioFile) []Chapter {
	if len(files) == 1 && len(files[0].Chapters) > 0 {
		return files[0].Chapters
	}
	var out []Chapter
	offset := 0.0
	for i, f := range files {
		if len(files) == 1 {
			return []Chapter{{ID: 0, Start: 0, End: f.Duration, Title: firstNonEmpty(f.Title, strings.TrimSuffix(f.Name, f.Ext))}}
		}
		title := f.Title
		if title == "" {
			title = strings.TrimSuffix(f.Name, f.Ext)
		}
		out = append(out, Chapter{ID: i, Start: offset, End: offset + f.Duration, Title: title})
		offset += f.Duration
	}
	return out
}

func totalDuration(files []AudioFile) float64 {
	t := 0.0
	for _, f := range files {
		t += f.Duration
	}
	return t
}

func totalSize(files []AudioFile) int64 {
	var t int64
	for _, f := range files {
		t += f.Size
	}
	return t
}

func tagValue(tags map[string]string, key string) string {
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func relTo(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.Base(p)
}

// inoFor makes a stable numeric-string id for a file from its path within the item —
// Audiobookshelf clients treat "ino" as an opaque string, and a number is the safe shape.
func inoFor(rel string) string {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(rel); i++ {
		h ^= uint64(rel[i])
		h *= 1099511628211
	}
	return strconv.FormatUint(h>>11, 10) // keep it within 53 bits: safe in JSON numbers too
}

func mimeFor(ext string) string {
	switch strings.ToLower(ext) {
	case ".m4b", ".m4a", ".mp4":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".opus":
		return "audio/ogg"
	case ".aac":
		return "audio/aac"
	case ".wav":
		return "audio/wav"
	case ".wma":
		return "audio/x-ms-wma"
	}
	return "application/octet-stream"
}

// naturalLess orders "Chapter 2" before "Chapter 10". It is library.NaturalLess — the one
// play order — so a book rename, which numbers tracks by it, can never reorder what plays.
func naturalLess(a, b string) bool { return library.NaturalLess(a, b) }

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// narratorFromTags picks a file's narrator from its tags. Audiobook taggers disagree on
// where it goes, so the first of these that says something wins: narrator, composer
// (the common audiobook convention), album_artist — only when it isn't the artist or the
// book's author, which is what most files put there — then performer.
func narratorFromTags(tags map[string]string, author string) string {
	if n := firstNonEmpty(tagValue(tags, "narrator"), tagValue(tags, "composer")); n != "" {
		return strings.TrimSpace(n)
	}
	if aa := tagValue(tags, "album_artist"); aa != "" && !strings.EqualFold(aa, tagValue(tags, "artist")) &&
		!strings.EqualFold(aa, strings.TrimSpace(author)) {
		return aa
	}
	return tagValue(tags, "performer")
}

// parseProbe reads ffprobe's JSON (format, first audio stream, chapters) into af. author
// is the book's, for narratorFromTags.
func parseProbe(out []byte, af *AudioFile, author string) bool {
	var r struct {
		Format struct {
			Duration string            `json:"duration"`
			BitRate  string            `json:"bit_rate"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Chapters []struct {
			StartTime string            `json:"start_time"`
			EndTime   string            `json:"end_time"`
			Tags      map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if json.Unmarshal(out, &r) != nil {
		return false
	}
	af.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	af.Bitrate, _ = strconv.Atoi(r.Format.BitRate)
	if len(r.Streams) > 0 {
		af.Codec = r.Streams[0].CodecName
	}
	af.Title = tagValue(r.Format.Tags, "title")
	af.Narrator = narratorFromTags(r.Format.Tags, author)
	af.Chapters = nil
	for i, c := range r.Chapters {
		s, _ := strconv.ParseFloat(c.StartTime, 64)
		e, _ := strconv.ParseFloat(c.EndTime, 64)
		title := tagValue(c.Tags, "title")
		if title == "" {
			title = "Chapter " + strconv.Itoa(i+1)
		}
		af.Chapters = append(af.Chapters, Chapter{ID: i, Start: s, End: e, Title: title})
	}
	return true
}
