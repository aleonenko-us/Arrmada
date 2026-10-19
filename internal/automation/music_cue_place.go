package automation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

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
// virtual files' place. ok is false when nothing should be placed: the split couldn't run
// now (no space, the music folder missing: tried again later) or failed (the album goes
// to Review). done deletes the pieces left once placement is over.
func (c *Coordinator) splitPlanned(ctx context.Context, al music.Album, plan []placement, releaseTier music.Quality, res *albumImport) (out []placement, done func(), ok bool) {
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
	if err := c.imp.CheckMusicRoot(); err != nil {
		res.failed += len(plan)
		c.log.Warn("music import: the music folder isn't there — not splitting the disc image", "album", al.Title, "err", err)
		return nil, done, false
	}
	plans := make([]stage.SplitPlan, len(imgs))
	for i, ci := range imgs {
		plans[i] = stage.SplitPlan{Image: ci.path, Cuts: ci.cuts, TotalSamples: ci.total,
			Channels: ci.info.Channels, Bits: ci.info.BitsPerSample, Tags: ci.tags}
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
