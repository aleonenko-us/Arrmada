package subtitles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// jobAudio is one job's decoded audio. Reading a remux's soundtrack off a spinning disk
// takes about a minute, so each audio stream is extracted to a 16 kHz WAV at most once
// per job, on first use, and shared: the sync check of every candidate in every
// language, language detection, and whisper. close() deletes the WAVs when the job ends.
type jobAudio struct {
	video   string
	mi      *mediaInfo // the probe; nil = the streams aren't known (ffmpeg's default is used)
	extract func(ctx context.Context, video string, stream int, dst string) error

	mu       sync.Mutex
	dir      string            // temp dir holding the WAVs; "" until the first extraction
	wavs     map[int]wavResult // by 0:a:N stream (-1 = ffmpeg's default)
	tracks   map[int]trackResult
	verdicts map[string]SyncCheck // lang|file id → the check a saved candidate passed
	closed   bool
}

type wavResult struct {
	path string
	err  error
}

type trackResult struct {
	t   *speechTrack
	err error
}

// newJobAudio prepares (but doesn't extract) a job's audio.
func (s *Service) newJobAudio(video string, mi *mediaInfo) *jobAudio {
	ex := s.extractAudio
	if ex == nil {
		ffmpeg := s.ffmpeg
		ex = func(ctx context.Context, video string, stream int, dst string) error {
			return extractWAV(ctx, ffmpeg, video, stream, dst)
		}
	}
	return &jobAudio{video: video, mi: mi, extract: ex}
}

// wav returns the extracted WAV for one audio stream, extracting it on first use. A
// failed extraction is remembered for the rest of the job (it won't work any better on
// the next candidate), except when the job was stopped.
func (a *jobAudio) wav(ctx context.Context, stream int) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", fmt.Errorf("audio already released")
	}
	if r, ok := a.wavs[stream]; ok {
		return r.path, r.err
	}
	if a.dir == "" {
		dir, err := os.MkdirTemp("", "arrmada-audio-*")
		if err != nil {
			return "", err
		}
		a.dir = dir
	}
	name := "default.wav"
	if stream >= 0 {
		name = fmt.Sprintf("a%d.wav", stream)
	}
	dst := filepath.Join(a.dir, name)
	err := a.extract(ctx, a.video, stream, dst)
	if err == nil {
		if fi, serr := os.Stat(dst); serr != nil || fi.Size() < 4096 {
			err = fmt.Errorf("extracted audio was empty (no decodable audio track?)")
		}
	}
	if err != nil {
		_ = os.Remove(dst)
		if ctx.Err() != nil {
			return "", ctx.Err() // stopped: not the audio's fault, so not remembered
		}
		dst = ""
	}
	if a.wavs == nil {
		a.wavs = map[int]wavResult{}
	}
	a.wavs[stream] = wavResult{dst, err}
	return dst, err
}

// track returns one stream's speech envelope, ready for sync checks.
func (a *jobAudio) track(ctx context.Context, stream int) (*speechTrack, error) {
	a.mu.Lock()
	if r, ok := a.tracks[stream]; ok {
		a.mu.Unlock()
		return r.t, r.err
	}
	a.mu.Unlock()
	path, err := a.wav(ctx, stream)
	if err != nil {
		return nil, err
	}
	t, err := loadSpeechTrack(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tracks == nil {
		a.tracks = map[int]trackResult{}
	}
	a.tracks[stream] = trackResult{t, err}
	return t, err
}

// syncStream is the audio a subtitle in lang is checked against: the stream the AI would
// transcribe for that language, else the default track (a Spanish subtitle on an English
// film follows the English speech; dubs are timed to the same picture).
func (a *jobAudio) syncStream(lang string) int {
	if st, _ := audioStreamFor(a.mi, lang, false); st >= 0 {
		return st
	}
	st, _ := audioStreamFor(a.mi, lang, true)
	return st
}

// remember keeps the check a saved candidate passed, for its provenance row.
func (a *jobAudio) remember(lang, fileID string, c SyncCheck) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.verdicts == nil {
		a.verdicts = map[string]SyncCheck{}
	}
	a.verdicts[strings.ToLower(lang)+"|"+fileID] = c
}

// verdict is the check remembered for a saved candidate, if it had one.
func (a *jobAudio) verdict(lang, fileID string) (SyncCheck, bool) {
	if a == nil {
		return SyncCheck{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.verdicts[strings.ToLower(lang)+"|"+fileID]
	return c, ok
}

// close deletes the job's WAVs. Safe on nil and more than once.
func (a *jobAudio) close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.dir != "" {
		_ = os.RemoveAll(a.dir)
		a.dir = ""
	}
	a.wavs, a.tracks = nil, nil
}
