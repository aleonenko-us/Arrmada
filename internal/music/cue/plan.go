package cue

// Where an image is cut.
//
// THE PREGAP RULE: a track runs from its INDEX 01 to the next track's INDEX 01, so the
// next track's pregap (its INDEX 00 to INDEX 01, usually a second or two of silence, or
// applause on a live album) stays at the END of the track before it. That's how a CD
// player plays the disc (it counts the gap down at the end of the previous track), what
// EAC, XLD and foobar2000 do by default, and what per-track rips of the same disc look
// like, so a split album matches them and plays gaplessly in order. INDEX 00 is read but
// never cut at.
//
// The audio before a file's first INDEX 01 has no previous track to join: it is its own
// segment, LeadIn. On a pressed CD that is track 1's pregap — normally two seconds of
// silence, sometimes a hidden track ("hidden track one audio", HTOA). The caller decides
// what to do with it; nothing here drops audio.

// SegmentKind says what a stretch of an image is.
type SegmentKind int

const (
	// SegTrack is an audio track.
	SegTrack SegmentKind = iota
	// SegLeadIn is the audio before the file's first INDEX 01.
	SegLeadIn
	// SegData is a data track's stretch (an enhanced CD's data session ripped into the
	// image): never a track.
	SegData
)

func (k SegmentKind) String() string {
	switch k {
	case SegLeadIn:
		return "lead-in"
	case SegData:
		return "data"
	}
	return "track"
}

// Segment is one stretch of an image file between two cuts.
type Segment struct {
	Kind  SegmentKind
	Track int   // index into Sheet.Tracks; -1 for the lead-in
	Start int64 // CD frames from the file's start
	End   int64 // CD frames; -1 runs to the end of the file
}

// TracksIn lists the tracks (indexes into s.Tracks) whose INDEX 01 is in file f, in order.
func (s *Sheet) TracksIn(f int) []int {
	var out []int
	for i, t := range s.Tracks {
		if t.Index01.File == f {
			out = append(out, i)
		}
	}
	return out
}

// IsImage reports whether file f is a whole-disc image: two or more audio tracks start in
// it. A file with one track is a track file of its own (a per-track rip's cue sheet, or
// a one-track single), whatever pregap it starts with, and is never cut.
func (s *Sheet) IsImage(f int) bool {
	n := 0
	for _, i := range s.TracksIn(f) {
		if s.Tracks[i].Audio() {
			n++
		}
	}
	return n >= 2
}

// HoldsAudio reports whether any audio track starts in file f (a file holding only a
// data track is skipped: it's usually not in the download at all).
func (s *Sheet) HoldsAudio(f int) bool {
	for _, i := range s.TracksIn(f) {
		if s.Tracks[i].Audio() {
			return true
		}
	}
	return false
}

// Segments cuts file f at each track's INDEX 01 (the pregap rule above). The segments
// cover the whole file, in order, with no gaps and no overlaps.
func (s *Sheet) Segments(f int) []Segment {
	idx := s.TracksIn(f)
	if len(idx) == 0 {
		return nil
	}
	var out []Segment
	if first := s.Tracks[idx[0]].Index01.Frames; first > 0 {
		out = append(out, Segment{Kind: SegLeadIn, Track: -1, Start: 0, End: first})
	}
	for k, i := range idx {
		seg := Segment{Kind: SegTrack, Track: i, Start: s.Tracks[i].Index01.Frames, End: -1}
		if !s.Tracks[i].Audio() {
			seg.Kind = SegData
		}
		if k+1 < len(idx) {
			seg.End = s.Tracks[idx[k+1]].Index01.Frames
		}
		out = append(out, seg)
	}
	return out
}

// Samples converts CD frames to samples at rate (Hz). Exact for every rate that is a
// multiple of 75 (44.1, 48, 88.2, 96, 176.4, 192 kHz); rounded to the nearest sample for
// the rest.
func Samples(frames int64, rate int) int64 {
	return (frames*int64(rate) + FramesPerSecond/2) / FramesPerSecond
}
