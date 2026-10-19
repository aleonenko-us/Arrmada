package automation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/cue"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// Placing a cue-sheet album's tracks (MUS-35; see music_cue.go): the matcher assigned
// virtual files, placement decided which tracks it takes, and only now is the image
// split. The pieces then replace the virtual files and go through placement as usual.

// probe is what a virtual track will be once split: a FLAC at the image's depth and rate,
// so the tier, the "keep what's held" decision and the naming templates see the file
// that will be placed.
func (p cuePart) probe() mediainfo.AudioInfo {
	info := mediainfo.AudioInfo{Codec: "flac", BitsPerSample: p.img.info.BitsPerSample,
		SampleRateHz: p.img.info.SampleRateHz, Channels: p.img.info.Channels, DurationSec: float64(p.ms) / 1000}
	if p.ms > 0 {
		info.BitrateKbps = int(p.size * 8 / int64(p.ms))
	}
	return info
}

// cueHiddenTracks offers a hidden track before track 1 (see hiddenTrackMin) to the
// edition's track 0 on the same disc, when MusicBrainz lists one. Otherwise it stays out
// of the import (splitPlanned logs it).
func (c *Coordinator) cueHiddenTracks(ctx context.Context, tracks []music.Track, matched map[int64]music.AudioFile) {
	cs := cueSetFrom(ctx)
	if cs == nil {
		return
	}
	byID := make(map[int64]music.Track, len(tracks))
	for _, t := range tracks {
		byID[t.ID] = t
	}
	for _, ci := range cs.images {
		if ci.hidden < 0 || ci.parts[ci.hidden] == "" {
			continue
		}
		disc, found := 0, false
		for id, f := range matched {
			if part, ok := cs.part(f.Path); ok && part.img == ci {
				disc, found = byID[id].DiscNumber, true
				break
			}
		}
		if !found {
			continue
		}
		for _, t := range tracks {
			if t.TrackNumber != 0 || t.DiscNumber != disc {
				continue
			}
			if _, taken := matched[t.ID]; !taken {
				p := cs.parts[ci.parts[ci.hidden]]
				matched[t.ID] = music.AudioFile{Path: ci.parts[ci.hidden], SizeBytes: p.size}
			}
		}
	}
}

// splitPlanned splits the images whose tracks the plan places and puts the pieces in the
// virtual files' place. tracks are the album's target tracks. ok is false when nothing
// should be placed: the split couldn't run now (no space, the music folder missing: tried
// again later) or shouldn't (the image's length isn't the disc's, the split didn't prove
// exact, AcoustID hears other recordings: the album goes to Review). done deletes the
// pieces left once placement is over.
func (c *Coordinator) splitPlanned(ctx context.Context, al music.Album, tracks []music.Track, plan []placement, releaseTier music.Quality, res *albumImport) (out []placement, done func(), ok bool) {
	done = func() {}
	cs := cueSetFrom(ctx)
	var imgs []*cueImage
	index := map[*cueImage]int{}
	for _, p := range plan {
		if part, ok := cs.part(p.f.Path); ok {
			if _, seen := index[part.img]; !seen {
				index[part.img] = len(imgs)
				imgs = append(imgs, part.img)
			}
		}
	}
	if len(imgs) == 0 {
		return plan, done, true
	}
	if why := cueLengthMismatch(cs, imgs, tracks, plan); why != "" {
		// The cue sheet's cuts were matched; the image's own length is what ties the
		// audio to the disc MusicBrainz lists.
		res.splitFailed++
		if res.splitWhy == "" {
			res.splitWhy = why
		}
		c.log.Warn("music import: the disc image's length isn't the disc's — not splitting it", "album", al.Title, "why", why)
		return nil, done, false
	}
	if err := c.imp.CheckMusicRoot(); err != nil {
		res.failed += len(plan)
		c.log.Warn("music import: the music folder isn't there — not splitting the disc image", "album", al.Title, "err", err)
		return nil, done, false
	}
	plans := make([]stage.SplitPlan, len(imgs))
	for i, ci := range imgs {
		plans[i] = stage.SplitPlan{Image: ci.path, Cuts: ci.cuts, TotalSamples: ci.total, Rate: ci.info.SampleRateHz,
			Channels: ci.info.Channels, Bits: ci.info.BitsPerSample, Layout: ci.info.ChannelLayout, Tags: ci.tags}
	}
	c.log.Info("music import: splitting a cue-sheet disc image into tracks", "album", al.Title, "images", len(imgs),
		"image", filepath.Base(imgs[0].path))
	job, results, err := c.splitImages(ctx, plans)
	if err != nil {
		switch {
		case ctx.Err() == nil && (errors.Is(err, stage.ErrSplit) || errors.Is(err, stage.ErrNoFFmpeg)):
			// Trying again would fail the same way: a person looks.
			res.splitFailed++
			if res.splitWhy == "" {
				res.splitWhy = err.Error()
			}
			c.log.Warn("music import: splitting the disc image failed — the download is untouched", "album", al.Title, "err", err)
		default:
			// No space, shutting down, the staging folder unwritable: later.
			res.failed += len(plan)
			c.log.Warn("music import: couldn't split the disc image now — trying again later", "album", al.Title, "err", err)
		}
		return nil, done, false
	}
	done = func() {
		if err := job.Abandon("import finished"); err != nil {
			c.log.Warn("music import: a split's leftover tracks were kept for the next start", "album", al.Title, "err", err)
		}
	}
	for i, r := range results {
		c.log.Info("music import: split a disc image into tracks", "album", al.Title, "image", filepath.Base(imgs[i].path),
			"tracks", len(r.Files), "peak_mb", r.PeakRSSKB>>10)
	}
	used := map[*cueImage]map[int]bool{}
	out = make([]placement, 0, len(plan))
	for _, p := range plan {
		part, ok := cs.part(p.f.Path)
		if !ok {
			out = append(out, p)
			continue
		}
		piece := results[index[part.img]].Files[part.seg]
		p.from, p.scratch = part.img.path, true
		p.f = music.AudioFile{Path: piece, SizeBytes: p.f.SizeBytes, Disc: p.f.Disc}
		p.tier, p.bitrate, p.probe = c.trackTier(ctx, piece, releaseTier)
		out = append(out, p)
		if used[part.img] == nil {
			used[part.img] = map[int]bool{}
		}
		used[part.img][part.seg] = true
	}
	if why := c.cueSoundsWrong(ctx, out); why != "" {
		done()
		res.splitFailed++
		if res.splitWhy == "" {
			res.splitWhy = why
		}
		c.log.Warn("music import: the split tracks don't sound like the album — not placing them", "album", al.Title, "why", why)
		return nil, func() {}, false
	}
	// Audio of the image that isn't imported is said so, never dropped silently.
	for i, ci := range imgs {
		for k, seg := range ci.segs {
			if used[ci][k] || seg.Kind == cue.SegTrack {
				continue
			}
			secs := fmt.Sprintf("%.1f", float64(results[i].Samples[k])/float64(ci.info.SampleRateHz))
			switch {
			case seg.Kind == cue.SegData:
				c.log.Info("music import: left out a data track ripped into the disc image", "image", filepath.Base(ci.path), "seconds", secs)
			case k == ci.hidden:
				c.log.Info("music import: left out a hidden track before track 1 — MusicBrainz lists no track 0 for this edition",
					"album", al.Title, "image", filepath.Base(ci.path), "seconds", secs)
			default:
				c.log.Info("music import: left out the disc's pregap before track 1", "image", filepath.Base(ci.path), "seconds", secs)
			}
		}
	}
	return out, done, true
}

// cueLengthTolerance is how far an image's track audio may be from the disc's listed
// length: a few seconds, plus half a second a track, since stored track lengths are whole
// seconds rounded down (so the expected length is taken half a second a track longer).
func cueLengthTolerance(tracks int) float64 { return 3 + 0.5*float64(tracks) }

// cueLengthMismatch checks each image against the disc its tracks were matched to: the
// image's real length (as probed, less what isn't a track) must be the length MusicBrainz
// lists for that disc, within cueLengthTolerance. A cue sheet only says where to cut; this
// is what says the audio is the disc's. "" when every image fits (or a disc has a track of
// unknown length, so there's nothing to compare).
func cueLengthMismatch(cs *cueSet, imgs []*cueImage, tracks []music.Track, plan []placement) string {
	for _, ci := range imgs {
		disc := -1
		for _, p := range plan {
			if part, ok := cs.part(p.f.Path); ok && part.img == ci && p.t.TrackNumber > 0 {
				disc = p.t.DiscNumber
				break
			}
		}
		if disc < 0 {
			continue
		}
		listed, n := 0, 0
		for _, t := range tracks {
			if t.DiscNumber != disc || t.TrackNumber == 0 {
				continue
			}
			if t.DurationSec <= 0 {
				listed = -1
				break
			}
			listed += t.DurationSec
			n++
		}
		if listed <= 0 {
			continue
		}
		var samples int64
		for k, seg := range ci.segs {
			if seg.Kind != cue.SegTrack {
				continue
			}
			start, end := int64(0), ci.total
			if k > 0 {
				start = ci.cuts[k-1]
			}
			if k < len(ci.cuts) {
				end = ci.cuts[k]
			}
			samples += end - start
		}
		have := float64(samples) / float64(ci.info.SampleRateHz)
		if math.Abs(have-(float64(listed)+0.5*float64(n))) > cueLengthTolerance(n) {
			return fmt.Sprintf("the disc image %s holds %s of tracks, but MusicBrainz lists %s for disc %d of this edition",
				filepath.Base(ci.path), clock(have), clock(float64(listed)), disc)
		}
	}
	return ""
}

func clock(sec float64) string {
	s := int(math.Round(sec))
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// cueSoundsWrong fingerprints up to two split tracks (when an AcoustID key is set) and
// says why when AcoustID clearly names other recordings for all it recognised: a cue
// sheet that matched by its text over audio that is something else. "" otherwise —
// including when AcoustID knows none of them, or can't be asked.
func (c *Coordinator) cueSoundsWrong(ctx context.Context, plan []placement) string {
	fp := c.fingerprinter
	if fp == nil || !fp.Enabled() {
		return ""
	}
	var picks []placement
	for _, p := range plan {
		if p.scratch && p.t.RecordingMBID != "" {
			picks = append(picks, p)
		}
	}
	if len(picks) > 2 {
		picks = []placement{picks[0], picks[len(picks)/2]}
	}
	heard, agreed := 0, 0
	var names []string
	for _, p := range picks {
		got, err := fp.Recordings(ctx, p.f.Path)
		if err != nil || len(got) == 0 {
			continue
		}
		heard++
		for _, g := range got {
			if strings.EqualFold(g, p.t.RecordingMBID) {
				agreed++
				break
			}
		}
		names = append(names, p.t.Title)
	}
	if heard == 0 || agreed > 0 {
		return ""
	}
	return fmt.Sprintf("AcoustID recognises the split audio as other recordings than %s", strings.Join(names, " and "))
}

// splitImages runs the split (stage.Split) in the music folder's staging area.
func (c *Coordinator) splitImages(ctx context.Context, plans []stage.SplitPlan) (*stage.Job, []stage.SplitResult, error) {
	return stage.Split(ctx, c.imp.MusicDir(), plans, c.stageDeps())
}

// musicCueReview holds a cue-sheet download whose image can't be split safely (a missing
// file, a cue that doesn't fit, an image that isn't lossless) for a person.
func (c *Coordinator) musicCueReview(ctx context.Context, it download.Item, idx string, albumID int64, why string) {
	title := ""
	if albumID > 0 {
		if al, err := c.music.AlbumRecord(ctx, albumID); err == nil {
			title = al.Title
		}
	}
	c.clearMusicRetry(it.Hash)
	c.log.Warn("music import: a cue-sheet album can't be split — sending to review", "release", it.Name, "why", why)
	c.addReview(ctx, Review{
		Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "music", ReasonCode: ReasonImportFailed,
		ExpectedID: albumID, ExpectedTitle: title, ParsedTitle: music.ParseRelease(it.Name).Album, SizeBytes: it.SizeBytes, Indexer: idx,
		Reason: why + " (the download is untouched)",
	})
}

// musicSplitReview sends a download to Review when its disc image was split and the
// pieces didn't prove to be the image exactly (or ffmpeg failed on it). Retrying would fail
// the same way; nothing was placed from the image, and the download is untouched.
func (c *Coordinator) musicSplitReview(ctx context.Context, it download.Item, res albumImport, albumID int64, title string) {
	c.clearMusicRetry(it.Hash)
	c.log.Warn("music import: the disc image couldn't be split exactly — sending to review", "what", title, "release", it.Name, "why", res.splitWhy)
	c.addReview(ctx, Review{
		Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "music", ReasonCode: ReasonImportFailed,
		ExpectedID: albumID, ExpectedTitle: title, SizeBytes: it.SizeBytes,
		Reason: "Splitting the cue-sheet disc image into tracks failed: " + res.splitWhy + " (the download is untouched)",
	})
}
