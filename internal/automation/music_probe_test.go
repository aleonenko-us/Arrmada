package automation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/music"
)

// probeByExt stands in for ffprobe: every .mp3 measures as a 245 kbps VBR MP3, every .flac
// as 16/44.1 FLAC. Paths it is asked about are counted.
func probeByExt(asked *[]string) func(context.Context, string) (mediainfo.AudioInfo, error) {
	return func(_ context.Context, path string) (mediainfo.AudioInfo, error) {
		*asked = append(*asked, path)
		switch strings.ToLower(filepath.Ext(path)) {
		case ".mp3":
			return mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 245, SampleRateHz: 44100}, nil
		case ".flac":
			return mediainfo.AudioInfo{Codec: "flac", BitsPerSample: 16, SampleRateHz: 44100, BitrateKbps: 900}, nil
		}
		return mediainfo.AudioInfo{}, errors.New("unreadable")
	}
}

// MUS-11: an import records what the files ARE, not what the release name says: a release
// labelled FLAC that holds MP3s is recorded as MP3-V0 with its bitrate.
func TestImportRecordsMeasuredTier(t *testing.T) {
	h, _ := musicImportHarness(t)
	var asked []string
	h.c.probeAudioFn = probeByExt(&asked)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".mp3", 1)
	if res.placed != 2 || len(asked) != 2 {
		t.Fatalf("placed %d, probed %d files", res.placed, len(asked))
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.Format != string(music.QualityMP3V0) || tr.BitrateKbps != 245 || tr.SourceRelease == "" {
			t.Errorf("track %q: %q at %d kbps (from %q), want MP3-V0 at 245 with the release kept", tr.Title, tr.Format, tr.BitrateKbps, tr.SourceRelease)
		}
		// MUS-29: the file row keeps every measured fact, not just the tier.
		if tr.Codec != "mp3" || tr.SampleRateHz != 44100 || tr.Lossless || tr.TagState != music.TagStateAsFound || tr.FileID == 0 {
			t.Errorf("track %q's file facts: codec %q, %d Hz, lossless %v, tags %q", tr.Title, tr.Codec, tr.SampleRateHz, tr.Lossless, tr.TagState)
		}
	}
	var files, probed int
	if err := h.st.DB().QueryRow(`SELECT COUNT(*), COUNT(probed_at) FROM music_track_files WHERE album_id = ?`, al.ID).Scan(&files, &probed); err != nil || files != 2 || probed != 2 {
		t.Errorf("music_track_files rows = %d, probed %d (err %v), want 2 and 2", files, probed, err)
	}
	// Nothing measured as FLAC-quality, so a real FLAC now replaces them.
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 2); res.placed != 2 {
		t.Errorf("a measured FLAC over measured MP3s: placed %d, want 2", res.placed)
	}
}

// Without a probe, a release tier that disagrees with the files (FLAC named, MP3s inside)
// isn't believed: the extension's tag is recorded instead.
func TestImportWithoutProbeFallsBackToExtension(t *testing.T) {
	h, _ := musicImportHarness(t)
	h.c.probeAudioFn = func(context.Context, string) (mediainfo.AudioInfo, error) {
		return mediainfo.AudioInfo{}, errNoFFprobe
	}
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer", "Kid A"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".mp3", 1)
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.Format != "MP3" || tr.BitrateKbps != 0 {
			t.Errorf("track %q: %q/%d, want the extension tag MP3", tr.Title, tr.Format, tr.BitrateKbps)
		}
	}
	// A release tier that agrees with the files is kept.
	kid := h.album(t, a.ID, "Kid A")
	h.importDownload(t, a, kid, "Radiohead - Kid A (2000) [MP3 320]", ".mp3", 1)
	tracks, _ = h.svc.Tracks(h.ctx, kid.ID)
	for _, tr := range tracks {
		if tr.Format != string(music.QualityMP3320) {
			t.Errorf("track %q: %q, want MP3-320 from the release", tr.Title, tr.Format)
		}
	}
}

// A held file recorded only as "MP3" (no bitrate) is measured before anything replaces it,
// so a worse copy can't slip in on a guess; the scan's backfill measures the rest.
func TestHeldUnknownIsMeasuredBeforeReplacing(t *testing.T) {
	h, _ := musicImportHarness(t)
	h.c.probeAudioFn = func(context.Context, string) (mediainfo.AudioInfo, error) {
		return mediainfo.AudioInfo{}, errNoFFprobe
	}
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	h.importDownload(t, a, al, "Radiohead - OK Computer (1997)", ".mp3", 1) // no tier named: recorded as "MP3", 0 kbps

	// Now the probe works: held files measure at 320 CBR; the new download at 128.
	h.c.probeAudioFn = func(_ context.Context, path string) (mediainfo.AudioInfo, error) {
		if strings.Contains(path, h.c.imp.MusicDir()) {
			return mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 320}, nil
		}
		return mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 128}, nil
	}
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [MP3 128]", ".mp3", 2); res.placed != 0 || res.kept != 2 {
		t.Fatalf("a 128 over measured 320s: placed %d kept %d, want 0 and 2", res.placed, res.kept)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.Format != string(music.QualityMP3320) || tr.BitrateKbps != 320 {
			t.Errorf("track %q: %q/%d, want measured MP3-320 recorded", tr.Title, tr.Format, tr.BitrateKbps)
		}
	}
}

// A file that can't be measured doesn't hold every later one back: the pass moves on.
func TestScanBackfillMovesPastUnmeasurable(t *testing.T) {
	h, _ := musicImportHarness(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	h.c.probeAudioFn = func(context.Context, string) (mediainfo.AudioInfo, error) { return mediainfo.AudioInfo{}, errNoFFprobe }
	h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 1)
	// The first track's file can't be read; the second can.
	h.c.probeAudioFn = func(_ context.Context, path string) (mediainfo.AudioInfo, error) {
		if strings.Contains(path, "One") {
			return mediainfo.AudioInfo{}, errors.New("unreadable")
		}
		return mediainfo.AudioInfo{Codec: "flac", BitsPerSample: 16, SampleRateHz: 44100, BitrateKbps: 900}, nil
	}
	got := 0
	for i := 0; i < 3; i++ {
		got += h.c.measureUnmeasuredTracks(h.ctx, 1)
	}
	if got != 1 {
		t.Errorf("measured %d over three passes of one, want the readable track", got)
	}
}

// The scan's backfill measures tracks recorded before probing existed, read-only, capped.
func TestScanBackfillMeasuresOldTracks(t *testing.T) {
	h, _ := musicImportHarness(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	h.c.probeAudioFn = func(context.Context, string) (mediainfo.AudioInfo, error) { return mediainfo.AudioInfo{}, errNoFFprobe }
	h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 1) // FLAC, 0 kbps

	var asked []string
	h.c.probeAudioFn = probeByExt(&asked)
	if n := h.c.measureUnmeasuredTracks(h.ctx, 1); n != 1 {
		t.Fatalf("measured %d with a cap of 1, want 1", n)
	}
	if n := h.c.measureUnmeasuredTracks(h.ctx, 200); n != 1 {
		t.Fatalf("second pass measured %d, want the 1 left", n)
	}
	if n := h.c.measureUnmeasuredTracks(h.ctx, 200); n != 0 {
		t.Errorf("third pass measured %d, want nothing left", n)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.Format != string(music.QualityFLAC) || tr.BitrateKbps != 900 || tr.Codec != "flac" || tr.BitDepth != 16 || !tr.Lossless {
			t.Errorf("track %q: %q/%d %q %d-bit, want FLAC at 900, measured 16-bit flac", tr.Title, tr.Format, tr.BitrateKbps, tr.Codec, tr.BitDepth)
		}
	}
}
