// Package filecheck looks inside the library's video files for what a release name can't
// promise: that there is sound, that the sound isn't silence, that it's in a language the
// household watches in, that the file isn't cut short, and that a "WEB-DL" grabbed weeks
// before the film was out at home isn't really a cam.
//
// A grab only ever judges a name, so a mislabelled cam or a file with a dead audio track
// gets into the library looking fine. The check runs on every import, as a nightly pass
// over the whole library (only files whose size or modification time changed), and on
// demand. What it finds is kept per file (file_check_findings) and shown in Needs you;
// the worst of it — a cam, no sound, silence, an unreadable file — is replaced
// automatically when the owner leaves "Replace bad copies automatically" on (auto.go).
package filecheck

import (
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// Finding codes. Stored, so never rename one.
const (
	CodeNoAudio       = "no_audio"       // no audio stream at all
	CodeSilentAudio   = "silent_audio"   // an audio stream, but silence wherever it was sampled
	CodeWrongLanguage = "wrong_language" // every tagged audio language is one nobody here watches in
	CodeSuspectedCam  = "suspected_cam"  // named a cam, or a "WEB-DL" grabbed before it was out at home
	CodeUnreadable    = "unreadable"     // ffprobe can't read it, or it's far shorter than the film
)

// Kinds of file.
const (
	KindMovie   = "movie"
	KindEpisode = "episode"
)

// Thresholds.
const (
	// SilentBelowDB: a window whose loudest moment is under this is silence. Real film
	// audio peaks far above it even in quiet scenes; a dead track reads -91 dB.
	SilentBelowDB = -50.0
	// CamTolerance is the grab rule's tolerance (quality.NotOutYetTolerance): a grab within
	// this of the home release may be a genuine early leak.
	CamTolerance = 3 * 24 * time.Hour
	// ShortFraction: a file under this share of the known runtime is cut short.
	ShortFraction = 0.5
	// minRuntimeForShort: the runtime must be known and at least this long to judge a
	// file's length by it (shorts and specials vary too much).
	minRuntimeForShort = 10
)

// Probe is what was read from a file.
type Probe struct {
	// Err is set when ffprobe couldn't read the file at all.
	Err error
	// AudioLangs are the audio streams' language tags, one per stream ("" untagged).
	AudioLangs  []string
	DurationSec float64
	// Loudness is the loudest moment (dB) of each sampled window of the first audio
	// stream; a window that couldn't be measured is left out.
	Loudness []float64
	// Windows is how many windows were sampled (Loudness has one per measured window).
	Windows int
}

// Input is a file and what is known about it besides its contents.
type Input struct {
	Kind    string
	Release string // the release name it was imported from ("" unknown)
	// Filename stands in for Release when that isn't known.
	Filename   string
	RuntimeMin int
	// OrigLanguage is the title's original language and Wanted the household's audio
	// languages (Convert's "keep audio"; English when empty).
	OrigLanguage string
	Wanted       []string
	// Home is a movie's home release (zero unknown) and GrabbedAt when the file was
	// grabbed (or, failing that, written); CamOK says its profile accepts cams, so nothing
	// is flagged as one.
	Home      time.Time
	GrabbedAt time.Time
	CamOK     bool
}

// Finding is one problem with a file: its code and the words for it ("the file has no
// sound"), which follow the title in a sentence.
type Finding struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Classify turns a probe of a file into its findings (none when it looks right).
func Classify(in Input, p Probe) []Finding {
	if p.Err != nil {
		return []Finding{{CodeUnreadable, "the file can't be read — it may be damaged or incomplete"}}
	}
	var out []Finding
	if p.DurationSec <= 0 {
		out = append(out, Finding{CodeUnreadable, "the file has no readable length — it may be damaged or incomplete"})
	} else if in.RuntimeMin >= minRuntimeForShort && p.DurationSec < float64(in.RuntimeMin*60)*ShortFraction {
		out = append(out, Finding{CodeUnreadable, fmt.Sprintf("the file is only %d minutes long but the %s runs %d — it's cut short",
			int(p.DurationSec/60), noun(in.Kind), in.RuntimeMin)})
	}
	switch {
	case len(p.AudioLangs) == 0:
		out = append(out, Finding{CodeNoAudio, "the file has no sound"})
	case silent(p):
		out = append(out, Finding{CodeSilentAudio, "the sound track is silent"})
	default:
		if only, ok := wrongLanguage(p.AudioLangs, in.OrigLanguage, in.Wanted); ok {
			out = append(out, Finding{CodeWrongLanguage, "audio is in " + only + " only"})
		}
	}
	if f, ok := suspectedCam(in); ok {
		out = append(out, f)
	}
	return out
}

func noun(kind string) string {
	if kind == KindEpisode {
		return "episode"
	}
	return "film"
}

// silent: every window was measured and none rose above SilentBelowDB. A window that
// couldn't be measured makes it unknown, never silent.
func silent(p Probe) bool {
	if p.Windows == 0 || len(p.Loudness) != p.Windows {
		return false
	}
	for _, db := range p.Loudness {
		if db >= SilentBelowDB {
			return false
		}
	}
	return true
}

// wrongLanguage reports a file whose tagged audio is all in languages that are neither
// the title's original language nor one of the household's, naming them ("Russian",
// "Russian and Ukrainian"). An untagged or "und" stream may be anything, so a file with
// one is never judged; nor is one whose tags aren't languages Arrmada knows.
func wrongLanguage(tags []string, orig string, wanted []string) (string, bool) {
	if len(wanted) == 0 {
		wanted = []string{"en"}
	}
	ok := map[string]bool{}
	for _, w := range wanted {
		if b := langs.AudioBase(w); b != "" {
			ok[b] = true
		}
	}
	if b := langs.AudioBase(orig); b != "" {
		ok[b] = true
	}
	var names []string
	seen := map[string]bool{}
	for _, t := range tags {
		b := langs.AudioBase(t)
		if b == "" {
			return "", false
		}
		if ok[b] {
			return "", false
		}
		if !seen[b] {
			seen[b] = true
			names = append(names, langs.Name(b))
		}
	}
	if len(names) == 0 {
		return "", false
	}
	if len(names) > 2 {
		return strings.Join(names[:2], ", ") + " and others", true
	}
	return strings.Join(names, " and "), true
}

// suspectedCam: the name says cam outright, or — a movie — it claims a home source but
// was grabbed more than CamTolerance before the film was out at home.
func suspectedCam(in Input) (Finding, bool) {
	if in.CamOK {
		return Finding{}, false
	}
	name := in.Release
	if name == "" {
		name = in.Filename
	}
	if name == "" {
		return Finding{}, false
	}
	src := parser.Parse(name).Source
	if src == parser.SourceCAM {
		return Finding{CodeSuspectedCam, "probably a cam — the release says so in its name"}, true
	}
	if in.Kind != KindMovie || in.Home.IsZero() || in.GrabbedAt.IsZero() || !homeSource(src) {
		return Finding{}, false
	}
	if early := in.Home.Sub(in.GrabbedAt); early > CamTolerance {
		return Finding{CodeSuspectedCam, "probably a cam — grabbed " + span(early) + " before it was out at home"}, true
	}
	return Finding{}, false
}

// homeSource is quality's: a source that only exists once a film is out at home.
func homeSource(s parser.Source) bool {
	switch s {
	case parser.SourceWebDL, parser.SourceWebRip, parser.SourceBluray, parser.SourceRemux, parser.SourceHDTV, parser.SourceDVD:
		return true
	}
	return false
}

// span is a duration in the words a person would use: "4 days", "3 weeks", "2 months".
func span(d time.Duration) string {
	days := int(d.Hours() / 24)
	plural := func(n int, w string) string {
		if n == 1 {
			return "1 " + w
		}
		return fmt.Sprintf("%d %ss", n, w)
	}
	switch {
	case days < 14:
		return plural(days, "day")
	case days < 60:
		return plural(days/7, "week")
	default:
		return plural(days/30, "month")
	}
}
