package convert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// Keeping Dolby Vision through a conversion — the "keep" choice (dvpolicy.go), a preview
// until it has been watched on a Dolby Vision TV. Dolby Vision is a per-frame metadata
// stream (the RPU) riding on an ordinary HDR10 picture, so it's carried around the encode the
// way HDR10+ is (hdr.go):
//
//  1. Read the source's RPU with dovi_tool. A profile 7 disc (dual layer, FEL or MEL) is
//     converted to profile 8.1 on the way (mode 2): 8.1 has no enhancement layer, so its
//     picture is the base layer's, which is the picture being encoded. Profile 5 has no
//     standard picture under its layer and is never converted (DVUnconvertible); a source
//     whose RPU says profile 5 when its container didn't is refused here.
//  2. Encode the picture to a raw HEVC stream without B-frames (encodeHEVCStream): a raw
//     stream is in display order only then, and the RPUs go back one per frame in order.
//  3. Count the encoded frames. Unless there is exactly one RPU per frame the original is
//     kept — a shifted RPU grades every later frame with another frame's metadata.
//  4. Inject the RPU (after HDR10+, when the base carries that too), then remux, writing
//     the container's Dolby Vision configuration record (remuxVideoStream).
//  5. Before the swap — after the SSIM check and with the full-decode check — the output's
//     own RPU is read back: profile 8, one per frame, the same metadata that went in, and a
//     configuration record saying profile 8.1 (an HDR10 base). Anything else keeps the
//     original.
//
// Always HEVC (dovi_tool writes HEVC), always the full frame (the RPU's letterbox offsets
// describe it), and always in one go: resumable pieces (chunk.go) are switched off for it —
// the RPU is per frame of the whole film, and that path isn't proven across joined pieces.

// dvRun is one job's extracted RPU.
type dvRun struct {
	rpu     string // the RPU file in scratch
	from    int    // the source RPU's profile (7 or 8)
	frames  int64  // RPUs in it, one per frame of the source's base layer
	summary string // dovi_tool's summary of it; the output's must read the same
}

// errDVMismatch means the RPU and the encoded frames don't pair up one for one.
var errDVMismatch = errors.New("the Dolby Vision metadata doesn't line up with the encoded frames")

// rpuSummary is `dovi_tool info --summary` read back.
type rpuSummary struct {
	frames  int64
	profile int
	text    string // the summary lines, for comparing two RPUs
}

var (
	rpuFramesRe  = regexp.MustCompile(`(?m)^\s*Frames:\s*(\d+)`)
	rpuProfileRe = regexp.MustCompile(`(?m)^\s*Profile:\s*(\d+)`)
)

// parseRPUSummary reads dovi_tool's summary. The lines after "Summary:" are kept as the
// comparable text (the "Parsing RPU file..." progress before it isn't).
func parseRPUSummary(out string) (rpuSummary, error) {
	var sum rpuSummary
	m := rpuFramesRe.FindStringSubmatch(out)
	p := rpuProfileRe.FindStringSubmatch(out)
	if m == nil || p == nil {
		return sum, fmt.Errorf("dovi_tool's summary has no frame count or profile (%s)", tailStr([]byte(out)))
	}
	sum.frames, _ = strconv.ParseInt(m[1], 10, 64)
	sum.profile, _ = strconv.Atoi(p[1])
	if i := strings.Index(out, "Summary:"); i >= 0 {
		out = out[i:]
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	sum.text = strings.Join(lines, "\n")
	return sum, nil
}

// rpuInfoFn summarises an RPU file; a seam for tests.
var rpuInfoFn = (*Service).rpuInfo

func (s *Service) rpuInfo(ctx context.Context, rpu string) (rpuSummary, error) {
	out, err := s.tool(ctx, nil, toolrun.Idle, s.doviTool, "info", "-i", rpu, "--summary").CombinedOutput()
	if err != nil {
		return rpuSummary{}, fmt.Errorf("dovi_tool info: %v (%s)", err, tailStr(out))
	}
	return parseRPUSummary(string(out))
}

// rpuFrameFn reads one frame's RPU as comparable data; a seam for tests.
var rpuFrameFn = (*Service).rpuFrame

// rpuFrame is `dovi_tool info --frame N`, parsed and normalised for comparing two RPUs of the
// same frame: the CRC (which covers the exact bytes) is dropped, and extension blocks are put
// in one order (their order carries no meaning, and a rewrite may change it).
func (s *Service) rpuFrame(ctx context.Context, rpu string, frame int64) (any, error) {
	out, err := s.tool(ctx, nil, toolrun.Idle, s.doviTool, "info", "-i", rpu, "--frame", strconv.FormatInt(frame, 10)).Output()
	if err != nil {
		return nil, fmt.Errorf("dovi_tool info --frame %d: %v", frame, err)
	}
	i := bytes.IndexByte(out, '{')
	if i < 0 {
		return nil, fmt.Errorf("dovi_tool info --frame %d printed no RPU", frame)
	}
	var v any
	if err := json.Unmarshal(out[i:], &v); err != nil {
		return nil, fmt.Errorf("dovi_tool info --frame %d: %v", frame, err)
	}
	return normaliseRPU(v), nil
}

func normaliseRPU(v any) any {
	switch t := v.(type) {
	case map[string]any:
		delete(t, "rpu_data_crc32")
		for k, e := range t {
			t[k] = normaliseRPU(e)
		}
		if blocks, ok := t["ext_metadata_blocks"].([]any); ok {
			sort.SliceStable(blocks, func(a, b int) bool {
				ja, _ := json.Marshal(blocks[a])
				jb, _ := json.Marshal(blocks[b])
				return string(ja) < string(jb)
			})
		}
	case []any:
		for i := range t {
			t[i] = normaliseRPU(t[i])
		}
	}
	return v
}

// sampleFrames is the frames the gate compares one by one: the first, the last and three
// between.
func sampleFrames(n int64) []int64 {
	if n <= 0 {
		return nil
	}
	var out []int64
	seen := map[int64]bool{}
	for _, f := range []int64{0, n / 4, n / 2, 3 * n / 4, n - 1} {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// extractRPU pipes path's video stream into dovi_tool and writes its RPU to out. mode is
// dovi_tool's -m ("" = as it is, "2" = to profile 8.1); limit stops after that many frames
// (0 = the whole film), when ffmpeg's broken pipe is expected rather than a failure.
func (s *Service) extractRPU(ctx context.Context, path string, videoIndex int, out, mode string, limit int) error {
	ff := s.tool(ctx, nil, toolrun.Idle, s.ffmpeg, "-loglevel", "error", "-i", path,
		"-map", fmt.Sprintf("0:v:%d", videoIndex), "-c", "copy", "-bsf:v", "hevc_mp4toannexb", "-f", "hevc", "-")
	var args []string
	if mode != "" {
		args = append(args, "-m", mode)
	}
	args = append(args, "extract-rpu")
	if limit > 0 {
		args = append(args, "-l", strconv.Itoa(limit))
	}
	args = append(args, "-", "-o", out)
	if err := pipeCommands(ff, s.tool(ctx, nil, toolrun.Idle, s.doviTool, args...), limit > 0); err != nil {
		return err
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		return fmt.Errorf("no Dolby Vision metadata found")
	}
	return nil
}

// readDolbyVision reads a source's RPU for the keep-Dolby-Vision pipeline. It looks before
// it reads: the first frames say which profile the RPU really is (the container's record
// can be missing or wrong), and only then is the whole film read, converted to 8.1 where it
// needs to be. On failure the job is finished here and ok is false.
func (s *Service) readDolbyVision(ctx context.Context, job *Job, src string, mi *MediaInfo, scratch string) (dv *dvRun, ok bool) {
	stem := filepath.Join(scratch, fmt.Sprintf("dv-%d", job.ID))
	peek, rpu := stem+".peek.bin", stem+".rpu"
	defer os.Remove(peek)
	failed := func(err error) (*dvRun, bool) {
		_ = os.Remove(rpu)
		switch {
		case ctx.Err() != nil:
		case errors.Is(err, errSourceStream):
			// Usually the disk; a damaged file fails the same way every time, so it counts.
			s.finishAfterEncode(job, SkipTransient, "couldn't read the whole file for its Dolby Vision metadata — will try again later")
		default:
			s.finishSkip(job, SkipHDRUnsupported, fmt.Sprintf("Dolby Vision metadata couldn't be read (%v) — kept the original rather than lose it", err))
		}
		return nil, false
	}
	s.event("info", fmt.Sprintf("%s: reading its Dolby Vision metadata…", job.Title))
	if err := s.extractRPU(ctx, src, mi.VideoIndex, peek, "", 1); err != nil {
		return failed(err)
	}
	first, err := rpuInfoFn(s, ctx, peek)
	if err != nil {
		return failed(err)
	}
	mode := ""
	switch first.profile {
	case 7:
		mode = "2" // to 8.1: the base layer's picture, the metadata converted to match it
	case 8:
	case 5:
		s.finishSkip(job, SkipHDRUnsupported, "Dolby Vision profile 5 — its picture can't be kept without the Dolby Vision layer, and that layer can't be carried through a conversion; kept the original")
		return nil, false
	default:
		s.finishSkip(job, SkipHDRUnsupported, fmt.Sprintf("Dolby Vision profile %d — only profiles 7 and 8 can keep their Dolby Vision layer; kept the original", first.profile))
		return nil, false
	}
	if err := s.extractRPU(ctx, src, mi.VideoIndex, rpu, mode, 0); err != nil {
		return failed(err)
	}
	all, err := rpuInfoFn(s, ctx, rpu)
	if err != nil {
		return failed(err)
	}
	if all.profile != 8 || all.frames <= 0 {
		return failed(fmt.Errorf("it read as profile %d with %d frames, not profile 8", all.profile, all.frames))
	}
	if first.profile == 7 {
		s.event("info", fmt.Sprintf("%s: Dolby Vision profile 7 — converting its metadata to profile 8.1 (the enhancement layer isn't kept)", job.Title))
	}
	return &dvRun{rpu: rpu, from: first.profile, frames: all.frames, summary: all.text}, true
}

// streamFramesFn counts the frames (packets) of a file's first video stream without
// decoding it; a seam for tests. format forces the demuxer ("hevc" for a raw stream).
var streamFramesFn = (*Service).streamFrames

func (s *Service) streamFrames(ctx context.Context, path, format string) (int64, error) {
	args := []string{"-v", "error"}
	if format != "" {
		args = append(args, "-f", format)
	}
	args = append(args, "-select_streams", "v:0", "-count_packets", "-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", path)
	out, err := s.tool(ctx, nil, toolrun.Idle, s.ffprobe, args...).Output()
	if err != nil {
		return 0, fmt.Errorf("counting frames: %w", err)
	}
	field, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	n, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("counting frames: %q", strings.TrimSpace(string(out)))
	}
	return n, nil
}

// dvAligned checks there is exactly one RPU per encoded frame, before anything is injected.
// dovi_tool would otherwise pad or drop to fit, and every frame after the gap would be
// graded with another frame's metadata.
func (s *Service) dvAligned(ctx context.Context, stream string, dv *dvRun) error {
	n, err := streamFramesFn(s, ctx, stream, "hevc")
	if err != nil {
		return err
	}
	if n != dv.frames {
		return fmt.Errorf("%w (%d frames of Dolby Vision metadata, %d encoded frames)", errDVMismatch, dv.frames, n)
	}
	return nil
}

// encodeDolbyVision runs the keep-Dolby-Vision pipeline into dst (see the top of the file).
// h10pJSON, when set, is the base layer's HDR10+ metadata, injected first.
func (s *Service) encodeDolbyVision(ctx context.Context, job *Job, src, dst, scratch string, mi *MediaInfo, plan Plan, h10pJSON string, dv *dvRun) error {
	if s.doviTool == "" || dv == nil {
		return fmt.Errorf("dovi_tool not available")
	}
	stem := filepath.Join(scratch, fmt.Sprintf("dv-enc-%d", job.ID))
	encoded, withH10p, injected := stem+".hevc", stem+".h10p.hevc", stem+".inj.hevc"
	defer os.Remove(encoded)
	defer os.Remove(withH10p)
	defer os.Remove(injected)

	// As for HDR10+: the stream gets what's left of the size cap after the copied audio.
	videoCap := int64(0)
	if limit := sizeCap(mi, plan); limit > 0 {
		videoCap = max(limit-keptAudioBytes(mi, plan), 1<<20)
	}
	if err := s.encodeHEVCStream(ctx, job, src, encoded, mi, plan, videoCap); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if videoCap > 0 && fileSize(encoded) >= videoCap {
		return errTooBig
	}
	stream := encoded
	if h10pJSON != "" {
		if s.hdr10plusTool == "" {
			return fmt.Errorf("hdr10plus_tool not available")
		}
		if out, err := s.tool(ctx, job, toolrun.Idle, s.hdr10plusTool, "inject", "-i", encoded, "-j", h10pJSON, "-o", withH10p).CombinedOutput(); err != nil {
			return fmt.Errorf("inject HDR10+: %v (%s)", err, tailStr(out))
		}
		_ = os.Remove(encoded)
		stream = withH10p
	}
	if err := s.dvAligned(ctx, stream, dv); err != nil {
		return err
	}
	if out, err := s.tool(ctx, job, toolrun.Idle, s.doviTool, "inject-rpu", "-i", stream, "--rpu-in", dv.rpu, "-o", injected).CombinedOutput(); err != nil {
		return fmt.Errorf("inject Dolby Vision: %v (%s)", err, tailStr(out))
	}
	_ = os.Remove(stream)
	// Exactly the original's frames, so its video start goes back as it was (hdr.go).
	if err := s.remuxVideoStream(ctx, injected, src, dst, mi, plan, startOffset(mi), true); err != nil {
		return fmt.Errorf("remux: %w", err)
	}
	return nil
}

// checkDolbyVision is the keep-Dolby-Vision gate on the finished file (finalizeOutput): its
// container says profile 8.1 over an HDR10 base, and its own RPU — read back out of the file
// that would be swapped in — is profile 8, one per frame, and the same metadata that went in.
// It returns the skip and why it fails ("" = it passed).
func (s *Service) checkDolbyVision(ctx context.Context, job *Job, dst string, out *MediaInfo, dv *dvRun) (kind, reason string) {
	if dv == nil {
		return SkipHDRUnsupported, "the Dolby Vision metadata read from the original went missing"
	}
	if out == nil || out.HDR != "Dolby Vision" || out.DVProfile != 8 || out.DVCompat != 1 {
		got := "no Dolby Vision"
		if out != nil && out.HDR == "Dolby Vision" {
			got = fmt.Sprintf("Dolby Vision profile %d (base compatibility %d)", out.DVProfile, out.DVCompat)
		}
		return SkipHDRUnsupported, "the converted file's container shows " + got + ", not profile 8.1"
	}
	s.event("info", fmt.Sprintf("%s: checking the converted file's Dolby Vision metadata…", job.Title))
	rpu := filepath.Join(filepath.Dir(dst), fmt.Sprintf("dv-check-%d.rpu", job.ID))
	defer os.Remove(rpu)
	if err := s.extractRPU(ctx, dst, 0, rpu, "", 0); err != nil {
		if ctx.Err() != nil {
			return SkipCancelled, "cancelled"
		}
		return SkipHDRUnsupported, fmt.Sprintf("the converted file's Dolby Vision metadata couldn't be read (%v)", err)
	}
	got, err := rpuInfoFn(s, ctx, rpu)
	if err != nil {
		// What was just read out of the file is what fails to parse: the file is at fault.
		return SkipHDRUnsupported, fmt.Sprintf("the converted file's Dolby Vision metadata doesn't read back (%v)", err)
	}
	frames, err := streamFramesFn(s, ctx, dst, "")
	if err != nil {
		return SkipTransient, fmt.Sprintf("the Dolby Vision check couldn't count the frames (%v)", err)
	}
	switch {
	case got.profile != 8:
		return SkipHDRUnsupported, fmt.Sprintf("the converted file's Dolby Vision metadata is profile %d, not 8", got.profile)
	case got.frames != frames || got.frames != dv.frames:
		return SkipHDRUnsupported, fmt.Sprintf("the converted file has %d frames of Dolby Vision metadata for %d frames (the original had %d)", got.frames, frames, dv.frames)
	case got.text != dv.summary:
		s.log.Warn("convert: Dolby Vision metadata changed on the way through", "title", job.Title, "in", dv.summary, "out", got.text)
		return SkipHDRUnsupported, "the converted file's Dolby Vision metadata doesn't match what was read from the original"
	}
	// The summary covers the film as a whole; a few frames are compared field by field too,
	// because the remux's filter rewrites every RPU (remuxVideoStream).
	for _, f := range sampleFrames(dv.frames) {
		want, err := rpuFrameFn(s, ctx, dv.rpu, f)
		if err != nil {
			return SkipTransient, fmt.Sprintf("the Dolby Vision check couldn't run (%v)", err)
		}
		have, err := rpuFrameFn(s, ctx, rpu, f)
		if err != nil {
			return SkipHDRUnsupported, fmt.Sprintf("the converted file's Dolby Vision metadata doesn't read back (%v)", err)
		}
		if !reflect.DeepEqual(want, have) {
			s.log.Warn("convert: a frame's Dolby Vision metadata changed on the way through", "title", job.Title, "frame", f)
			return SkipHDRUnsupported, fmt.Sprintf("frame %d's Dolby Vision metadata in the converted file doesn't match the original's", f)
		}
	}
	s.event("info", fmt.Sprintf("%s: Dolby Vision kept — profile 8.1, %d frames of metadata, one per frame", job.Title, got.frames))
	return "", ""
}
