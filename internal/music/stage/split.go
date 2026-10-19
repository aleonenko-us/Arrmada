package stage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// Splitting a cue-sheet image (MUS-35). A whole-disc rip (one FLAC, WAV, APE or WavPack
// file plus a .cue) is cut into one FLAC per track inside a staging job of its own (Kind
// "split"), and those files are then imported like any download's tracks: copied into an
// import job, house-tagged, checked and moved in. The image in the download is only read.
//
// How: one ffmpeg run per image, decoding it once and cutting the decoded samples with the
// asegment filter at exact sample offsets (CD frame × rate / 75), each piece encoded to
// FLAC at the image's own bit depth. A filter cut on decoded audio is exact to the sample;
// seeking (-ss) is not.
//
// The proof: the image and the pieces are both decoded to raw PCM, and the pieces, joined
// in order, must hash the same as the image and have exactly the planned number of
// samples each. Anything else and the split is thrown away and the album goes to Review.
//
// The pieces are scratch: an interrupted split is deleted at the next start (Recover:
// nothing in a split job ever reached the library), and a finished one is deleted once
// the import is done (Abandon). Each piece is also deleted as soon as its tagged copy is
// in the library (Item.Scratch), so a split costs about one image's size in staging.

// ErrSplit is a split that failed or didn't prove exact: the pieces are deleted, the image
// is untouched, and the album needs a person.
var ErrSplit = errors.New("splitting the image failed")

// ErrNoFFmpeg: splitting needs ffmpeg, and it isn't installed.
var ErrNoFFmpeg = errors.New("splitting a cue-sheet image needs ffmpeg, which isn't installed")

// SplitPlan is one image to cut.
type SplitPlan struct {
	// Image is the file in the download. Only ever read.
	Image string
	// Cuts are the sample offsets where each piece after the first starts: strictly
	// increasing and above zero. The image becomes len(Cuts)+1 pieces.
	Cuts []int64
	// TotalSamples is the image's length in samples (per channel), as probed. Only used to
	// refuse cuts past the end; the check measures the real length by decoding.
	TotalSamples int64
	// Channels and Bits are the image's (Bits: its real sample depth, 8 to 24).
	Channels int
	Bits     int
	// Tags are written into each piece (ffmpeg metadata keys: title, artist, album,
	// album_artist, track, disc, date, genre). nil or short is fine.
	Tags []map[string]string
}

// pieces is how many files the plan makes.
func (p SplitPlan) pieces() int { return len(p.Cuts) + 1 }

// want is how many samples piece k should hold, in an image of total samples.
func (p SplitPlan) want(k int, total int64) int64 {
	start, end := int64(0), total
	if k > 0 {
		start = p.Cuts[k-1]
	}
	if k < len(p.Cuts) {
		end = p.Cuts[k]
	}
	return end - start
}

func (p SplitPlan) validate() error {
	if p.Channels <= 0 {
		return fmt.Errorf("%w: the image's channel count is unknown", ErrSplit)
	}
	if p.Bits < 8 || p.Bits > 24 {
		// 0 is a depth the probe couldn't read (a guess could truncate 24-bit audio);
		// above 24 is 32-bit or float audio, which FLAC here doesn't take.
		return fmt.Errorf("%w: the image's sample depth (%d bits) isn't one that's split", ErrSplit, p.Bits)
	}
	var prev int64
	for _, c := range p.Cuts {
		if c <= prev {
			return fmt.Errorf("%w: the cut points aren't in order", ErrSplit)
		}
		prev = c
	}
	if p.TotalSamples > 0 && prev >= p.TotalSamples {
		return fmt.Errorf("%w: a track starts after the end of the image", ErrSplit)
	}
	return nil
}

// SplitResult is one image's pieces, in order.
type SplitResult struct {
	Files   []string
	Samples []int64
}

// splitTimeout bounds one image's split and its check. FLAC decodes and encodes at
// hundreds of times real time; at idle priority on a busy disk, much slower.
const splitTimeout = 2 * time.Hour

// Split cuts each image into FLAC pieces in a new split job under libraryRoot's staging
// folder, then proves the pieces are the image, sample for sample. On success the caller
// owns the job and Abandons it once the pieces are imported (or not); on failure nothing
// is left behind.
func Split(ctx context.Context, libraryRoot string, plans []SplitPlan, d Deps) (*Job, []SplitResult, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, nil, ErrNoFFmpeg
	}
	m := Manifest{Kind: KindSplit}
	var need uint64
	for _, p := range plans {
		if err := p.validate(); err != nil {
			return nil, nil, err
		}
		fi, err := os.Stat(p.Image)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrSplit, err)
		}
		need += splitNeed(p.Image, fi.Size())
		for k := 0; k < p.pieces(); k++ {
			m.Items = append(m.Items, Item{Source: p.Image})
		}
	}
	j, err := Start(libraryRoot, m, d)
	if err != nil {
		return nil, nil, err
	}
	if err := j.checkFree(need); err != nil {
		_ = j.Abandon("no space")
		return nil, nil, err
	}
	out := make([]SplitResult, len(plans))
	first := 0
	for i, p := range plans {
		items := j.Items()[first : first+p.pieces()]
		files := make([]string, len(items))
		for k, it := range items {
			files[k] = it.Staged
		}
		res, err := j.splitOne(ctx, p, files)
		if err != nil {
			_ = j.Abandon("split failed")
			return nil, nil, err
		}
		for k := range items {
			if err := j.set(first+k, func(it *Item) { it.State = Verified }); err != nil {
				_ = j.Abandon("split failed")
				return nil, nil, err
			}
		}
		out[i] = res
		first += p.pieces()
	}
	return j, out, nil
}

// splitNeed is how much room an image's pieces take: about the image for FLAC and WAV
// (a WAV shrinks), a quarter more for formats that pack tighter than FLAC (APE, WavPack).
func splitNeed(image string, size int64) uint64 {
	switch strings.ToLower(filepath.Ext(image)) {
	case ".flac", ".wav", ".aiff", ".aif":
		return uint64(size)
	}
	return uint64(size) + uint64(size)/4
}

// checkFree refuses when the staging drive can't take need bytes plus the usual margin.
func (j *Job) checkFree(need uint64) error {
	if need == 0 {
		return nil
	}
	free := j.d.FreeBytes
	if free == nil {
		free = func(p string) (uint64, bool) {
			u, ok := diskspace.Of(p)
			return u.FreeBytes, ok
		}
	}
	have, ok := free(j.dir)
	if !ok {
		return nil
	}
	margin := uint64(64<<20) + need/50
	if have < need+margin {
		return fmt.Errorf("%w: %d MB needed, %d MB free", ErrNoSpace, (need+margin)>>20, have>>20)
	}
	return nil
}

// splitOne runs ffmpeg for one image and checks its pieces.
func (j *Job) splitOne(ctx context.Context, p SplitPlan, files []string) (SplitResult, error) {
	ctx, cancel := context.WithTimeout(ctx, splitTimeout)
	defer cancel()
	out, err := toolrun.Command(ctx, toolrun.Idle, "ffmpeg", SplitArgs(p, files)...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return SplitResult{}, ctx.Err()
		}
		return SplitResult{}, fmt.Errorf("%w: ffmpeg: %v: %s", ErrSplit, err, lastLine(out))
	}
	for i := range files {
		if fi, err := os.Stat(files[i]); err != nil || fi.Size() == 0 {
			return SplitResult{}, fmt.Errorf("%w: ffmpeg made no piece %d", ErrSplit, i+1)
		}
	}
	return verifySplit(ctx, p, files)
}

// SplitArgs is the ffmpeg command line that cuts p into files (one per piece): decode once,
// cut the samples with asegment, encode each piece to FLAC at the image's depth, with only
// the tags given (nothing copied from the image: its tags describe the whole disc, and its
// embedded pictures aren't audio).
func SplitArgs(p SplitPlan, files []string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-n", "-i", p.Image}
	cuts := make([]string, len(p.Cuts))
	for i, c := range p.Cuts {
		cuts[i] = strconv.FormatInt(c, 10)
	}
	var graph strings.Builder
	graph.WriteString("[0:a:0]asegment=samples=")
	graph.WriteString(strings.Join(cuts, "|"))
	for k := range files {
		fmt.Fprintf(&graph, "[s%d]", k)
	}
	args = append(args, "-filter_complex", graph.String())
	for k, f := range files {
		args = append(args, "-map", fmt.Sprintf("[s%d]", k), "-map_metadata", "-1", "-c:a", "flac")
		if p.Bits > 16 {
			args = append(args, "-bits_per_raw_sample", strconv.Itoa(p.Bits))
		}
		if k < len(p.Tags) {
			for _, key := range []string{"title", "artist", "album", "album_artist", "track", "disc", "date", "genre"} {
				if v := strings.TrimSpace(p.Tags[k][key]); v != "" {
					args = append(args, "-metadata", key+"="+v)
				}
			}
		}
		args = append(args, "-fflags", "+bitexact", "-flags:a", "+bitexact", "-f", "flac", f)
	}
	return args
}

// verifySplit decodes the image and the pieces to raw PCM and checks the pieces, in order,
// are the image exactly: the same samples, each piece the planned length.
func verifySplit(ctx context.Context, p SplitPlan, files []string) (SplitResult, error) {
	whole := sha256.New()
	total, err := decodePCM(ctx, p, p.Image, whole)
	if err != nil {
		return SplitResult{}, err
	}
	if len(p.Cuts) > 0 && p.Cuts[len(p.Cuts)-1] >= total {
		return SplitResult{}, fmt.Errorf("%w: a track starts after the end of the image (%d samples)", ErrSplit, total)
	}
	joined := sha256.New()
	res := SplitResult{Files: files}
	var sum int64
	for k, f := range files {
		n, err := decodePCM(ctx, p, f, joined)
		if err != nil {
			return SplitResult{}, err
		}
		if want := p.want(k, total); n != want {
			return SplitResult{}, fmt.Errorf("%w: piece %d has %d samples, not %d", ErrSplit, k+1, n, want)
		}
		res.Samples = append(res.Samples, n)
		sum += n
	}
	if sum != total {
		return SplitResult{}, fmt.Errorf("%w: the pieces hold %d samples, the image %d", ErrSplit, sum, total)
	}
	if !bytes.Equal(whole.Sum(nil), joined.Sum(nil)) {
		return SplitResult{}, fmt.Errorf("%w: the pieces' audio isn't the image's", ErrSplit)
	}
	return res, nil
}

// decodePCM decodes path's first audio stream to raw little-endian PCM (16-bit, or 32-bit
// for deeper audio, so 24-bit samples keep every bit), writes it to h and says how many
// samples per channel it held.
func decodePCM(ctx context.Context, p SplitPlan, path string, h hash.Hash) (int64, error) {
	format, width := "s16le", 2
	if p.Bits > 16 {
		format, width = "s32le", 4
	}
	cmd := toolrun.Command(ctx, toolrun.Idle, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", path, "-map", "0:a:0", "-c:a", "pcm_"+format, "-f", format, "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	n, cerr := io.Copy(h, pipe)
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if cerr != nil || werr != nil {
		return 0, fmt.Errorf("%w: decoding %s: %v %v: %s", ErrSplit, filepath.Base(path), cerr, werr, lastLine(stderr.Bytes()))
	}
	frame := int64(width * p.Channels)
	if n%frame != 0 {
		return 0, fmt.Errorf("%w: %s decoded to a partial sample", ErrSplit, filepath.Base(path))
	}
	return n / frame, nil
}

func lastLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// SplitFiles lists a split job's pieces in order (their staged paths).
func (j *Job) SplitFiles() []string {
	var out []string
	for _, it := range j.Items() {
		out = append(out, it.Staged)
	}
	return out
}

// isScratch reports whether path is a piece inside a split job in the same staging folder
// as this job (never this job's own folder, never anything outside a job's files/).
func (j *Job) isScratch(path string) bool {
	files := filepath.Dir(path)
	job := filepath.Dir(files)
	return filepath.Base(files) == filesDir && reJobID.MatchString(filepath.Base(job)) &&
		filepath.Clean(filepath.Dir(job)) == filepath.Clean(filepath.Dir(j.dir)) && filepath.Clean(job) != filepath.Clean(j.dir)
}

// dropScratch deletes an item's source once its copy is in the library, when the source
// is a split piece (Item.Scratch): it was only ever ours, and keeping every piece until
// the whole album is in would cost a second image's worth of space.
func (j *Job) dropScratch(it Item) {
	if !it.Scratch || !j.isScratch(it.Source) {
		return
	}
	if err := os.Remove(it.Source); err != nil && !errors.Is(err, os.ErrNotExist) {
		j.d.Log.Warn("music: couldn't delete a split track already placed", "path", it.Source, "err", err)
	}
}
