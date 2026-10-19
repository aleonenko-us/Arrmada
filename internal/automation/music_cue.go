package automation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/cue"
)

// Cue-sheet albums (MUS-35): a download holding one audio image per disc and a .cue sheet
// saying where each track starts. Plex doesn't read cue sheets, so the image is split
// into one FLAC per track and those are imported like any album's files.
//
// The split is put off until it's known to be worth it. First each image stands in for
// its tracks as "virtual" files — one per track, named, tagged and timed from the cue
// sheet (the titles and INDEX points make a strong match) — and the matcher weighs those.
// Only when it is confident, and only for the tracks placement will actually take (not
// ones a better copy already holds), is the image split, in the music folder's staging
// area (stage.Split: ffmpeg, sample-exact, proven). The pieces then go through the usual
// staging import — house tags, checks, one move — and are deleted once placed. The
// download is only ever read.
//
// What is never guessed, and goes to Reviews instead: a cue naming a file that isn't in
// the download, a cue that doesn't fit its image (a track starting past the end), an
// image that isn't lossless, a split that doesn't prove exact.
//
// Not split: an image whose tracks are already in the download as files beside it (some
// releases ship both), and a cue sheet that describes per-track files (each FILE one
// track): those files are imported as they are.

// hiddenTrackMin is how long the audio before track 1 must be to be a hidden track
// ("hidden track one audio") rather than the disc's pregap, normally two seconds of
// silence.
const hiddenTrackMin = 4 * cue.FramesPerSecond

// cueImage is one image a cue sheet cuts.
type cueImage struct {
	path  string // the image in the download (only read)
	sheet string // its cue sheet
	info  mediainfo.AudioInfo
	total int64 // samples per channel, from the probe
	segs  []cue.Segment
	cuts  []int64 // sample offsets of segments[1:]
	tags  []map[string]string
	parts []string // per segment: its virtual path ("" for audio no track takes)
	disc  int
	// hidden is the segment of a hidden track before track 1 (-1: none).
	hidden int
}

// cuePart is one virtual file: a segment of an image.
type cuePart struct {
	img  *cueImage
	seg  int
	tags music.FileTags
	ms   int
	size int64
}

// cueSet is what one download's cue sheets came to. nil: none.
type cueSet struct {
	parts  map[string]cuePart
	images []*cueImage
}

func (cs *cueSet) part(path string) (cuePart, bool) {
	if cs == nil {
		return cuePart{}, false
	}
	p, ok := cs.parts[path]
	return p, ok
}

type cueSetKey struct{}

// withCueSet carries a download's cue sheets to the matcher and placement.
func withCueSet(ctx context.Context, cs *cueSet) context.Context {
	if cs == nil {
		return ctx
	}
	return context.WithValue(ctx, cueSetKey{}, cs)
}

func cueSetFrom(ctx context.Context) *cueSet {
	cs, _ := ctx.Value(cueSetKey{}).(*cueSet)
	return cs
}

// cueProblem is a cue-sheet album that can't be imported without a person: the reason
// is shown in Reviews.
type cueProblem struct{ why string }

func (p *cueProblem) Error() string { return p.why }

func cueTrouble(format string, args ...any) error {
	return &cueProblem{why: fmt.Sprintf(format, args...)}
}

// expandCueSheets finds the cue sheets under root and puts each image they cut in files'
// place as one virtual file per track. files is what findAudioFiles found there. A cue
// sheet that can't be read is ignored (the files are matched as they are); an image
// that can't be split safely is a *cueProblem.
func (c *Coordinator) expandCueSheets(ctx context.Context, root string, files []music.AudioFile) ([]music.AudioFile, *cueSet, error) {
	sheets := findCueSheets(root)
	if len(sheets) == 0 {
		return files, nil, nil
	}
	// First every sheet's images, found or not, so a folder's other audio files can be
	// told from the images beside them.
	type ref struct {
		sheetPath string
		s         *cue.Sheet
		file, n   int    // the FILE, and which of the sheet's images it is
		of        int    // how many images the sheet cuts
		img       string // "" when it isn't in the download
	}
	var refs []ref
	images := map[string]bool{}
	for _, sheetPath := range sheets {
		raw, err := os.ReadFile(sheetPath)
		if err != nil {
			continue
		}
		s, err := cue.Parse(raw)
		if err != nil {
			c.log.Info("music import: a cue sheet couldn't be read — the files are matched as they are", "cue", filepath.Base(sheetPath), "err", err)
			continue
		}
		var cut []int
		for f := range s.Files {
			if s.HoldsAudio(f) && s.IsImage(f) {
				cut = append(cut, f)
			}
		}
		for n, f := range cut {
			r := ref{sheetPath: sheetPath, s: s, file: f, n: n, of: len(cut)}
			r.img = resolveCueFile(root, sheetPath, s.Files[f].Name, len(cut) == 1)
			if r.img != "" {
				images[r.img] = true
			}
			refs = append(refs, r)
		}
	}
	cs := &cueSet{parts: map[string]cuePart{}}
	drop := map[string]bool{} // images replaced by their tracks, or redundant
	claimed := map[string]bool{}
	for _, r := range refs {
		tracks := 0
		for _, i := range r.s.TracksIn(r.file) {
			if r.s.Tracks[i].Audio() {
				tracks++
			}
		}
		if r.img == "" {
			// Per-track rips often carry the ripper's whole-disc cue sheet too, naming an
			// image that was never kept: the track files beside it are what there is. One
			// lone file beside it is not a rip of tracks: that goes to Reviews.
			if audioBeside(filepath.Dir(r.sheetPath), files, images) >= 2 {
				continue
			}
			return nil, nil, cueTrouble("The cue sheet %s names %q, which isn't in the download", filepath.Base(r.sheetPath), r.s.Files[r.file].Name)
		}
		if claimed[r.img] {
			continue // a second cue sheet for the same image ("Album.cue" and "Album (EAC).cue")
		}
		claimed[r.img] = true
		if audioBeside(filepath.Dir(r.img), files, images) >= tracks {
			c.log.Info("music import: the image's tracks are in the download as files already — not splitting it",
				"image", filepath.Base(r.img), "cue", filepath.Base(r.sheetPath))
			drop[r.img] = true
			continue
		}
		disc := r.s.DiscNumber
		if r.of > 1 {
			disc = r.n + 1
		}
		if disc == 0 {
			disc = discHint(r.sheetPath, r.img)
		}
		ci, err := c.cueImageOf(ctx, r.s, r.file, r.sheetPath, r.img, disc)
		if err != nil {
			return nil, nil, err
		}
		drop[r.img] = true
		cs.images = append(cs.images, ci)
	}
	if len(cs.images) == 0 && len(drop) == 0 {
		return files, nil, nil
	}
	out := make([]music.AudioFile, 0, len(files))
	discOf := map[string]int{}
	for _, f := range files {
		if drop[filepath.Clean(f.Path)] {
			discOf[filepath.Clean(f.Path)] = f.Disc
			continue
		}
		out = append(out, f)
	}
	taken := map[string]bool{}
	for _, f := range out {
		taken[strings.ToLower(f.Path)] = true
	}
	for _, ci := range cs.images {
		ci.parts = make([]string, len(ci.segs))
		for k := range ci.segs {
			p, ok := c.cuePartOf(ci, k)
			if !ok {
				continue
			}
			path := virtualName(filepath.Dir(ci.path), p.tags, ci.disc > 0 && len(cs.images) > 1, taken)
			ci.parts[k] = path
			cs.parts[path] = p
			if k == ci.hidden {
				continue // offered to a track 0 only (placement), never to the matcher
			}
			out = append(out, music.AudioFile{Path: path, SizeBytes: p.size, Disc: discOf[filepath.Clean(ci.path)]})
		}
	}
	if len(cs.images) == 0 {
		return out, nil, nil
	}
	return out, cs, nil
}

// cueImageOf reads one image's facts and cuts it by the sheet.
func (c *Coordinator) cueImageOf(ctx context.Context, s *cue.Sheet, f int, sheetPath, img string, disc int) (*cueImage, error) {
	info, err := c.probeAudio(ctx, img)
	if err != nil {
		if errors.Is(err, errNoFFprobe) {
			return nil, cueTrouble("%s is a disc image with a cue sheet; splitting it needs ffmpeg, which isn't installed", filepath.Base(img))
		}
		return nil, cueTrouble("Couldn't read the disc image %s: %v", filepath.Base(img), err)
	}
	if q := music.TierFromProbe(info); q == music.QualityUnknown || !music.IsLosslessQuality(q) {
		return nil, cueTrouble("%s is a %s disc image: only lossless images (FLAC, WAV, APE, WavPack) are split into tracks", filepath.Base(img), strings.ToUpper(info.Codec))
	}
	if info.SampleRateHz <= 0 || info.Channels <= 0 || info.DurationSec <= 0 {
		return nil, cueTrouble("Couldn't read the length or format of the disc image %s", filepath.Base(img))
	}
	if info.BitsPerSample < 8 || info.BitsPerSample > 24 {
		return nil, cueTrouble("%s is %d-bit audio, which isn't split (FLAC here takes up to 24 bits)", filepath.Base(img), info.BitsPerSample)
	}
	ci := &cueImage{path: img, sheet: sheetPath, info: info, disc: disc, hidden: -1,
		total: int64(math.Round(info.DurationSec * float64(info.SampleRateHz)))}
	ci.segs = s.Segments(f)
	for k, seg := range ci.segs {
		if k == 0 {
			continue
		}
		at := cue.Samples(seg.Start, info.SampleRateHz)
		// Every track must start inside the image and leave its last track at least a
		// second: a cue that runs past the end is for another rip of the disc.
		if at >= ci.total-int64(info.SampleRateHz) {
			num := 0
			if seg.Track >= 0 {
				num = s.Tracks[seg.Track].Number
			}
			return nil, cueTrouble("The cue sheet %s doesn't fit %s: track %d starts at %s, but the image is only %s long",
				filepath.Base(sheetPath), filepath.Base(img), num, cue.FormatTime(seg.Start), cue.FormatTime(ci.total*cue.FramesPerSecond/int64(info.SampleRateHz)))
		}
		ci.cuts = append(ci.cuts, at)
	}
	// Tags for each track, from the sheet.
	audio := 0
	for _, seg := range ci.segs {
		if seg.Kind == cue.SegTrack {
			audio++
		}
	}
	pos := 0
	ci.tags = make([]map[string]string, len(ci.segs))
	for k, seg := range ci.segs {
		switch seg.Kind {
		case cue.SegLeadIn:
			if seg.End >= hiddenTrackMin {
				ci.hidden = k
				ci.tags[k] = cueTags(s, nil, 0, audio, disc)
			}
			continue
		case cue.SegData:
			continue
		}
		pos++
		ci.tags[k] = cueTags(s, &s.Tracks[seg.Track], pos, audio, disc)
	}
	return ci, nil
}

// cueTags are a track's tags from its cue sheet, as ffmpeg metadata keys.
func cueTags(s *cue.Sheet, t *cue.Track, pos, of, disc int) map[string]string {
	m := map[string]string{"album": s.Title, "album_artist": s.Performer, "artist": s.Performer, "date": s.Date, "genre": s.Genre}
	if t != nil {
		m["title"] = t.Title
		if t.Performer != "" {
			m["artist"] = t.Performer
		}
		if t.ISRC != "" {
			m["isrc"] = t.ISRC
		}
	}
	m["track"] = strconv.Itoa(pos)
	if of > 0 {
		m["tracktotal"] = strconv.Itoa(of)
	}
	if disc > 0 {
		m["disc"] = strconv.Itoa(disc)
	}
	if s.TotalDiscs > 0 {
		m["disctotal"] = strconv.Itoa(s.TotalDiscs)
	}
	if s.Catalog != "" {
		m["barcode"] = s.Catalog
	}
	return m
}

// cuePartOf is segment k of an image as a virtual file. Lead-ins (unless a hidden track)
// and data tracks aren't files.
func (c *Coordinator) cuePartOf(ci *cueImage, k int) (cuePart, bool) {
	seg := ci.segs[k]
	if seg.Kind == cue.SegData || (seg.Kind == cue.SegLeadIn && k != ci.hidden) || ci.tags[k] == nil {
		return cuePart{}, false
	}
	start, end := int64(0), ci.total
	if k > 0 {
		start = ci.cuts[k-1]
	}
	if k < len(ci.cuts) {
		end = ci.cuts[k]
	}
	n := end - start
	m := ci.tags[k]
	num := func(key string) int { v, _ := strconv.Atoi(m[key]); return v }
	p := cuePart{img: ci, seg: k, ms: int(n * 1000 / int64(ci.info.SampleRateHz))}
	if fi, err := os.Stat(ci.path); err == nil && ci.total > 0 {
		p.size = fi.Size() * n / ci.total
	}
	p.tags = music.FileTags{
		Title: m["title"], Artist: m["artist"], AlbumArtist: m["album_artist"], Album: m["album"],
		Track: num("track"), TrackTotal: num("tracktotal"), Disc: num("disc"), DiscTotal: num("disctotal"),
		Date: m["date"], ISRC: m["isrc"], Barcode: m["barcode"],
	}
	if k == ci.hidden {
		p.tags.Track = 0
	}
	return p, true
}

// virtualName names a virtual track file in its image's folder: "03 - Title.flac" (with
// the disc, "2-03 - Title.flac", when one sheet cuts several images), never the name of a
// real file there or another virtual one.
func virtualName(dir string, t music.FileTags, withDisc bool, taken map[string]bool) string {
	title := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, strings.TrimSpace(t.Title))
	if r := []rune(title); len(r) > 80 {
		title = string(r[:80])
	}
	if title == "" {
		title = "Track"
	}
	base := fmt.Sprintf("%02d - %s", t.Track, title)
	if withDisc {
		base = fmt.Sprintf("%d-%02d - %s", t.Disc, t.Track, title)
	}
	for i := 1; ; i++ {
		name := base + ".flac"
		if i > 1 {
			name = fmt.Sprintf("%s (cue %d).flac", base, i)
		}
		p := filepath.Join(dir, name)
		if _, err := os.Lstat(p); err != nil && !taken[strings.ToLower(p)] {
			taken[strings.ToLower(p)] = true
			return p
		}
	}
}

// findCueSheets lists the .cue files under root (none for a single-file download).
// Hidden folders are skipped, as are files too big to be a cue sheet.
func findCueSheets(root string) []string {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".cue") || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > cue.MaxSize {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out
}

// resolveCueFile finds the file a cue sheet's FILE line names, beside the sheet: the name
// as written; the same name in another case; the same name with another lossless
// audio extension (rippers write the cue for the .wav, then compress it to .flac); and —
// when the sheet cuts one image only — the audio file named like the cue sheet itself.
// Never a file outside root. "" when none.
func resolveCueFile(root, sheetPath, name string, single bool) string {
	dir := filepath.Dir(sheetPath)
	want := filepath.Clean(filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))))
	if !insideDir(want, root) {
		return ""
	}
	if fi, err := os.Stat(want); err == nil && fi.Mode().IsRegular() {
		return want
	}
	// look lists dir for a file named name in any case, and an audio file with stem.
	look := func(dir, name, stem string) (exact, sameStem string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", ""
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			if name != "" && strings.EqualFold(e.Name(), name) {
				exact = filepath.Join(dir, e.Name())
			}
			// Any audio file: a lossy one is found, and then refused with that reason.
			if music.IsAudioFile(e.Name()) && sameStem == "" && strings.EqualFold(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), stem) {
				sameStem = filepath.Join(dir, e.Name())
			}
		}
		return exact, sameStem
	}
	base := filepath.Base(want)
	if exact, other := look(filepath.Dir(want), base, strings.TrimSuffix(base, filepath.Ext(base))); exact != "" {
		return exact
	} else if other != "" {
		return other
	}
	if !single {
		return ""
	}
	_, other := look(dir, "", strings.TrimSuffix(filepath.Base(sheetPath), filepath.Ext(sheetPath)))
	return other
}

// audioBeside counts the audio files in dir that aren't disc images: a folder holding as
// many as an image has tracks already has the album as track files.
func audioBeside(dir string, files []music.AudioFile, images map[string]bool) int {
	n := 0
	for _, af := range files {
		if filepath.Dir(af.Path) == filepath.Clean(dir) && !images[filepath.Clean(af.Path)] {
			n++
		}
	}
	return n
}

// reDiscName finds a disc number in a file name: "Album CD2.cue", "Album (Disc 1).flac".
var reDiscName = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:cd|dis[ck])[\s._-]*0*(\d{1,2})(?:[^0-9]|$)`)

// discHint is the disc a cue sheet's or its image's name gives (0: neither says).
func discHint(names ...string) int {
	for _, n := range names {
		base := filepath.Base(n)
		if m := reDiscName.FindStringSubmatch(strings.TrimSuffix(base, filepath.Ext(base))); m != nil {
			if d, _ := strconv.Atoi(m[1]); d > 0 {
				return d
			}
		}
	}
	return 0
}
