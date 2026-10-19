package stage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// Splitting a cue-sheet image (MUS-35). A whole-disc rip (one FLAC, WAV, APE or WavPack
// file plus a .cue) is cut into one FLAC per track inside a staging job of its own (Kind
// "split"), and those files are then imported like any download's tracks: copied into an
// import job, house-tagged, checked and moved in. The image in the download is only read.
//
// How: ffmpeg decodes the image once, to raw 32-bit PCM on a pipe (no resampling, no
// remixing). Arrmada reads that stream and cuts it at exact sample counts (CD frame × rate
// / 75), feeding each piece to its own ffmpeg FLAC encoder, one at a time, at the image's
// own depth. Memory stays at a pipe's buffer or two however long the image is: an earlier
// version cut with one ffmpeg filter graph feeding every output at once, and that held most
// of the decoded disc in memory (half a gigabyte for a CD, gigabytes for 24/192).
//
// The proof: while the image streams through, its samples are hashed; afterwards each piece
// is decoded again (to 32-bit, so no bit escapes the comparison) and the pieces, joined in
// order, must hash the same and hold exactly their planned number of samples. A FLAC image
// is also checked against the MD5 of its samples its own header carries, so a damaged
// image isn't split. Anything else and the split is thrown away and the album goes to
// Review.
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
	// refuse cuts past the end; the split measures the real length as it decodes.
	TotalSamples int64
	// Rate, Channels and Bits are the image's (Bits: its real sample depth, 8 to 24).
	// Layout is its channel layout as ffmpeg names it ("stereo"); "" picks the usual one
	// for the channel count.
	Rate     int
	Channels int
	Bits     int
	Layout   string
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
	if p.Channels <= 0 || p.Rate <= 0 {
		return fmt.Errorf("%w: the image's channel count or sample rate is unknown", ErrSplit)
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

// layout is the channel layout the raw stream is declared with.
func (p SplitPlan) layout() string {
	switch {
	case p.Layout != "" && !strings.Contains(p.Layout, "unknown"):
		return p.Layout
	case p.Channels == 1:
		return "mono"
	case p.Channels == 2:
		return "stereo"
	}
	return strconv.Itoa(p.Channels) + "c"
}

// SplitResult is one image's pieces, in order.
type SplitResult struct {
	Files   []string
	Samples []int64
	// PeakRSSKB is the most memory any ffmpeg process of the split used (kilobytes; 0
	// where the platform doesn't say).
	PeakRSSKB int64
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

// pcmFrame is the size of one sample of every channel in the decoded stream (32-bit).
func (p SplitPlan) pcmFrame() int { return 4 * p.Channels }

// decodeArgs decodes path's first audio stream to raw 32-bit little-endian PCM on stdout:
// no resampling, no remixing, every bit of a 24-bit sample kept.
func decodeArgs(path string) []string {
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", path, "-map", "0:a:0",
		"-c:a", "pcm_s32le", "-f", "s32le", "-"}
}

// encodeArgs encodes one piece, read as raw PCM from stdin, to FLAC at the image's depth:
// 16-bit and shallower as s16le (Arrmada narrows the 32-bit stream, losslessly), deeper as
// s32le with the real depth declared so FLAC stores exactly those bits. Only the given
// tags are written; nothing of the image's.
func encodeArgs(p SplitPlan, k int, file string) []string {
	in := "s32le"
	if p.Bits <= 16 {
		in = "s16le"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-n",
		"-f", in, "-ar", strconv.Itoa(p.Rate), "-ch_layout", p.layout(), "-i", "-",
		"-map_metadata", "-1", "-c:a", "flac"}
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
	return append(args, "-fflags", "+bitexact", "-flags:a", "+bitexact", "-f", "flac", file)
}

// splitOne cuts one image into files and proves the pieces.
func (j *Job) splitOne(ctx context.Context, p SplitPlan, files []string) (SplitResult, error) {
	ctx, cancel := context.WithTimeout(ctx, splitTimeout)
	defer cancel()
	res := SplitResult{Files: files}
	image := sha256.New()
	info, _ := readStreamInfo(p.Image)
	var md *streamMD5
	if info.ok && info.bits == p.Bits && info.md5 != ([16]byte{}) {
		md = newStreamMD5(info.bits)
	}

	dec := toolrun.Command(ctx, toolrun.Idle, "ffmpeg", decodeArgs(p.Image)...)
	var decErr bytes.Buffer
	dec.Stderr = &limitedBuffer{b: &decErr, max: 64 << 10}
	pipe, err := dec.StdoutPipe()
	if err != nil {
		return res, err
	}
	if err := dec.Start(); err != nil {
		return res, err
	}
	finishDecoder := func() error {
		werr := dec.Wait()
		res.PeakRSSKB = max(res.PeakRSSKB, maxRSSKB(dec.ProcessState))
		return werr
	}
	fail := func(err error) (SplitResult, error) {
		stopped := ctx.Err() // a shutdown or the time limit, before our own cancel below
		cancel()             // stops the decoder (its whole group)
		_ = finishDecoder()
		if stopped != nil && !errors.Is(err, ErrNoSpace) {
			return SplitResult{}, stopped
		}
		return SplitResult{}, err
	}

	r := bufio.NewReaderSize(pipe, 1<<20)
	frame := p.pcmFrame()
	buf := make([]byte, frame*8192)
	narrow := make([]byte, len(buf)/2)
	var total int64
	for k, file := range files {
		limit := int64(-1) // the last piece runs to the end
		if k < len(p.Cuts) {
			limit = p.want(k, 0) // set by its two cuts alone
		}
		n, rss, err := j.encodePiece(ctx, p, k, file, r, limit, buf, narrow, image, md)
		res.PeakRSSKB = max(res.PeakRSSKB, rss)
		if err != nil {
			return fail(err)
		}
		if limit >= 0 && n != limit {
			// The stream ended early: the decoder failed, or the cue runs past the image.
			if derr := finishDecoder(); derr != nil {
				return SplitResult{}, fmt.Errorf("%w: decoding the image: %v: %s", ErrSplit, derr, lastLine(decErr.Bytes()))
			}
			return SplitResult{}, fmt.Errorf("%w: the image ends before piece %d's planned end (the cue sheet runs past it)", ErrSplit, k+1)
		}
		res.Samples = append(res.Samples, n)
		total += n
	}
	if err := finishDecoder(); err != nil {
		if ctx.Err() != nil {
			return SplitResult{}, ctx.Err()
		}
		return SplitResult{}, fmt.Errorf("%w: decoding the image: %v: %s", ErrSplit, err, lastLine(decErr.Bytes()))
	}
	if md != nil {
		if got := md.sum(); got != info.md5 {
			return SplitResult{}, fmt.Errorf("%w: the image's audio doesn't match the checksum in its own header — it's damaged", ErrSplit)
		}
		if info.samples > 0 && info.samples != total {
			return SplitResult{}, fmt.Errorf("%w: the image decodes to %d samples, its header says %d", ErrSplit, total, info.samples)
		}
	}
	return res, verifyPieces(ctx, p, files, image.Sum(nil), &res)
}

// encodePiece streams up to limit samples (-1: to the end) from r into a new ffmpeg FLAC
// encoder writing file, hashing them on the way. It returns how many samples it took.
func (j *Job) encodePiece(ctx context.Context, p SplitPlan, k int, file string, r io.Reader, limit int64,
	buf, narrow []byte, image hash.Hash, md *streamMD5) (int64, int64, error) {
	enc := toolrun.Command(ctx, toolrun.Idle, "ffmpeg", encodeArgs(p, k, file)...)
	var encErr bytes.Buffer
	enc.Stderr = &limitedBuffer{b: &encErr, max: 64 << 10}
	in, err := enc.StdinPipe()
	if err != nil {
		return 0, 0, err
	}
	if err := enc.Start(); err != nil {
		return 0, 0, err
	}
	frame := p.pcmFrame()
	var samples int64
	var werr, rerr error
	for limit < 0 || samples < limit {
		want := len(buf)
		if limit >= 0 && int64(want/frame) > limit-samples {
			want = int(limit-samples) * frame
		}
		n, err := io.ReadFull(r, buf[:want])
		if n%frame != 0 {
			rerr = fmt.Errorf("%w: the image decoded to a partial sample", ErrSplit)
			break
		}
		if n > 0 {
			chunk := buf[:n]
			image.Write(chunk)
			if md != nil {
				md.write(chunk)
			}
			out := chunk
			if p.Bits <= 16 {
				out = narrow[:n/2]
				for i := 0; i < n; i += 4 {
					// The top two bytes of a little-endian 32-bit sample are the 16-bit
					// sample. Anything in the low two is caught by the proof.
					out[i/2], out[i/2+1] = chunk[i+2], chunk[i+3]
				}
			}
			if _, err := in.Write(out); err != nil {
				werr = err
				break
			}
			samples += int64(n / frame)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				rerr = err
			}
			break
		}
	}
	_ = in.Close()
	waitErr := enc.Wait()
	rss := maxRSSKB(enc.ProcessState)
	if diskFull(werr, waitErr, encErr.Bytes()) {
		return samples, rss, fmt.Errorf("%w: the disk filled up while splitting", ErrNoSpace)
	}
	if rerr != nil {
		return samples, rss, rerr
	}
	if werr != nil || waitErr != nil {
		if ctx.Err() != nil {
			return samples, rss, ctx.Err()
		}
		return samples, rss, fmt.Errorf("%w: encoding piece %d: %v %v: %s", ErrSplit, k+1, werr, waitErr, lastLine(encErr.Bytes()))
	}
	return samples, rss, nil
}

// diskFull reports whether a piece failed because the staging drive filled up: worth
// trying again later, not a Review.
func diskFull(werr, waitErr error, stderr []byte) bool {
	return errors.Is(werr, syscall.ENOSPC) || errors.Is(waitErr, syscall.ENOSPC) ||
		bytes.Contains(bytes.ToLower(stderr), []byte("no space left on device"))
}

// verifyPieces decodes every piece (to 32-bit) and checks that, joined in order, they are
// exactly the image's samples, each the length it was cut to.
func verifyPieces(ctx context.Context, p SplitPlan, files []string, want []byte, res *SplitResult) error {
	joined := sha256.New()
	for k, f := range files {
		n, rss, err := decodePCM(ctx, p, f, joined)
		res.PeakRSSKB = max(res.PeakRSSKB, rss)
		if err != nil {
			return err
		}
		if n != res.Samples[k] {
			return fmt.Errorf("%w: piece %d decodes to %d samples, not the %d cut", ErrSplit, k+1, n, res.Samples[k])
		}
	}
	if !bytes.Equal(want, joined.Sum(nil)) {
		return fmt.Errorf("%w: the pieces' audio isn't the image's", ErrSplit)
	}
	return nil
}

// decodePCM decodes path's first audio stream to raw 32-bit little-endian PCM, writes it
// to h and says how many samples per channel it held.
func decodePCM(ctx context.Context, p SplitPlan, path string, h hash.Hash) (int64, int64, error) {
	cmd := toolrun.Command(ctx, toolrun.Idle, "ffmpeg", decodeArgs(path)...)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedBuffer{b: &stderr, max: 64 << 10}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, 0, err
	}
	n, cerr := io.Copy(h, pipe)
	werr := cmd.Wait()
	rss := maxRSSKB(cmd.ProcessState)
	if ctx.Err() != nil {
		return 0, rss, ctx.Err()
	}
	if cerr != nil || werr != nil {
		return 0, rss, fmt.Errorf("%w: decoding %s: %v %v: %s", ErrSplit, filepath.Base(path), cerr, werr, lastLine(stderr.Bytes()))
	}
	frame := int64(p.pcmFrame())
	if n%frame != 0 {
		return 0, rss, fmt.Errorf("%w: %s decoded to a partial sample", ErrSplit, filepath.Base(path))
	}
	return n / frame, rss, nil
}

// limitedBuffer keeps the first max bytes written to it (a tool's error output).
type limitedBuffer struct {
	b   *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
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

// streamInfo is what a FLAC file's STREAMINFO block says about its audio.
type streamInfo struct {
	ok      bool
	bits    int
	samples int64
	md5     [16]byte
}

// readStreamInfo reads a FLAC file's STREAMINFO (ok false for anything else).
func readStreamInfo(path string) (streamInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return streamInfo{}, err
	}
	defer f.Close()
	var hdr [4 + 4 + 34]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return streamInfo{}, nil
	}
	// "fLaC", then the first metadata block, which must be STREAMINFO (type 0, 34 bytes).
	if string(hdr[:4]) != "fLaC" || hdr[4]&0x7f != 0 || int(hdr[5])<<16|int(hdr[6])<<8|int(hdr[7]) != 34 {
		return streamInfo{}, nil
	}
	si := hdr[8:]
	// Bytes 10..17: 20 bits rate, 3 bits channels-1, 5 bits depth-1, 36 bits samples.
	x := binary.BigEndian.Uint64(si[10:18])
	out := streamInfo{ok: true, bits: int((x>>36)&0x1f) + 1, samples: int64(x & (1<<36 - 1))}
	copy(out.md5[:], si[18:34])
	return out, nil
}

// streamMD5 computes the MD5 FLAC's STREAMINFO carries: every sample, signed little-endian,
// interleaved, in as many bytes as its depth needs — here from the 32-bit decoded stream.
type streamMD5 struct {
	h     hash.Hash
	bits  int
	width int
	out   []byte
}

func newStreamMD5(bits int) *streamMD5 {
	return &streamMD5{h: md5.New(), bits: bits, width: (bits + 7) / 8}
}

func (s *streamMD5) write(chunk []byte) {
	n := len(chunk) / 4 * s.width
	if cap(s.out) < n {
		s.out = make([]byte, n)
	}
	out := s.out[:n]
	shift := 32 - s.bits
	for i, o := 0, 0; i+4 <= len(chunk); i, o = i+4, o+s.width {
		v := int32(binary.LittleEndian.Uint32(chunk[i:])) >> shift
		for b := 0; b < s.width; b++ {
			out[o+b] = byte(v >> (8 * b))
		}
	}
	s.h.Write(out)
}

func (s *streamMD5) sum() (out [16]byte) {
	copy(out[:], s.h.Sum(nil))
	return out
}

// SplitFiles lists a split job's pieces in order (their staged paths).
func (j *Job) SplitFiles() []string {
	var out []string
	for _, it := range j.Items() {
		out = append(out, it.Staged)
	}
	return out
}

// isScratch reports whether path is a piece of a split job (Kind "split") in the same
// staging folder as this job: never this job's own folder, never anything outside a
// split job's files/.
func (j *Job) isScratch(path string) bool {
	files := filepath.Dir(path)
	job := filepath.Dir(files)
	if filepath.Base(files) != filesDir || !reJobID.MatchString(filepath.Base(job)) ||
		filepath.Clean(filepath.Dir(job)) != filepath.Clean(filepath.Dir(j.dir)) || filepath.Clean(job) == filepath.Clean(j.dir) {
		return false
	}
	m, err := readManifest(job)
	return err == nil && m.Kind == KindSplit
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
