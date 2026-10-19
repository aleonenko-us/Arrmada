package library

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// RenameSource is what a library file's new name is built from when it's renamed: its
// current name, the release it was imported from (when one was recorded) and what reading
// the file itself found ("" for anything that wasn't read).
type RenameSource struct {
	FileName      string // the file's current base name
	SourceRelease string // the release it was imported from; "" for a scanned or hand-placed file
	Resolution    string // probed: "2160p", "1080p", "720p", "576p", "480p" or "SD"
	Codec         string // probed video codec, as mediainfo ("x265", "AV1") or Convert ("hevc", "av1") names it
}

// Release is the release a renamed file is named from.
//
// A library name keeps only what the scheme of its day asked for: a film imported as
// "American Psycho (2000).mkv" has lost its release's quality, codec and group for good, so
// a rename that read only the file name could never fill tokens added to the scheme later.
// The recorded release still has them. It is the download's name, though, and an import
// names a file from the media file inside the download, so a rename can now produce a
// fuller (or differently spelled) name than the import did. The rule:
//
//   - The release is trusted only when nothing the file's name states contradicts it: a
//     different resolution (or a different probed one), group, source or edition means the
//     record describes another file — one swapped in outside Arrmada, or a release guessed
//     by an old backfill — and the file's name alone is used. The name was the only true
//     record of that file; a rename must never overwrite it with another file's facts.
//   - A trusted release fills the resolution, source and group the name doesn't state.
//   - The codec is the probed one when the file was read (Convert re-encodes files), else
//     the one the name states, else the release's. The name beats the release because
//     Convert's facts are keyed by path and a rename loses them until the next index: a
//     name written from the probe must not be pulled back to a release Convert never
//     restamped.
//   - The edition never comes from the release. Editions belong to tracks: the track's
//     edition, else whatever the file's name already says.
//   - Season and episode numbers always come from the file's name (a season pack's release
//     names no episode), and the title from the library's metadata.
func (s RenameSource) Release() parser.Release {
	file := parser.Parse(s.FileName)
	file.Group = nameGroup(s.FileName, file)
	rel := file
	if src := strings.TrimSpace(s.SourceRelease); src != "" {
		if r := parser.Parse(src); s.describes(r, file) {
			rel = overlayRelease(file, r)
		}
	}
	if c := probedCodec(s.Codec); c != parser.CodecUnknown {
		rel.Codec = c
	}
	return rel
}

// ReleaseFits reports whether the recorded release can be about the file: there is one, and
// nothing the file's name (or probed resolution) states contradicts it. A rescan that finds
// a file under a new name asks this before letting it keep the old record's release.
func (s RenameSource) ReleaseFits() bool {
	src := strings.TrimSpace(s.SourceRelease)
	if src == "" {
		return false
	}
	file := parser.Parse(s.FileName)
	file.Group = nameGroup(s.FileName, file)
	return s.describes(parser.Parse(src), file)
}

// ReleaseConfirmed is ReleaseFits where the file's name also states the release's own
// resolution or group: positive evidence, for a caller with no other way to tell the file
// is the one the release was recorded for.
func (s RenameSource) ReleaseConfirmed() bool {
	if !s.ReleaseFits() {
		return false
	}
	file, r := parser.Parse(s.FileName), parser.Parse(strings.TrimSpace(s.SourceRelease))
	if g := nameGroup(s.FileName, file); g != "" && strings.EqualFold(g, r.Group) {
		return true
	}
	res := file.StatedResolution()
	return res != parser.ResUnknown && res == r.StatedResolution()
}

// describes reports whether release r can be about this file: nothing the file's name
// states — resolution (or the probed one), group, source, edition — says otherwise. A fact
// only one side states is no contradiction. The codec is left out: Convert changes it.
func (s RenameSource) describes(r, file parser.Release) bool {
	if stated := r.StatedResolution(); stated != parser.ResUnknown {
		have := s.Resolution
		if have == "" {
			have = string(file.StatedResolution())
		}
		if have != "" && resolutionClass(have) != resolutionClass(string(stated)) {
			return false
		}
	}
	if file.Group != "" && r.Group != "" && !strings.EqualFold(file.Group, r.Group) {
		return false
	}
	if fs, rs := statedSource(file), statedSource(r); fs != "" && rs != "" && fs != rs {
		return false
	}
	if fe, re := parser.NormalizeEdition(file.Edition), parser.NormalizeEdition(r.Edition); fe != "" && re != "" && fe != re {
		return false
	}
	return true
}

// reNumbering is a "group" that is really an episode or part number.
var reNumbering = regexp.MustCompile(`(?i)^(s\d+)?e\d+$|^\d+$`)

// tagWords are release tags that end a name after a dash without being a group: the tail
// of "WEB-DL" (the default scheme's own output, "Tenet (2020) - 1080p WEB-DL"), and picture,
// audio and scene tags the parser reads elsewhere in a name.
var tagWords = map[string]bool{
	"dl": true, "rip": true, "ray": true, "10bit": true, "8bit": true, "12bit": true, "sdr": true,
	"hdr": true, "hdr10": true, "hdr10plus": true, "dv": true, "dovi": true, "hlg": true, "hd": true,
	"uhd": true, "remux": true, "proper": true, "repack": true, "real": true, "internal": true,
	"limited": true, "multi": true, "dual": true, "dubbed": true, "subbed": true, "complete": true,
	"extended": true, "uncut": true, "unrated": true, "imax": true, "remastered": true,
}

// nameGroup is the release group library file name (parsed as file) states, or "" when
// what the parser read as one is really part of the name's other tokens. Library names end
// in a dash before the quality ("Bluray-1080p", Radarr's style), a second episode
// ("S01E01-E02"), or a tag ("1080p WEB-DL"), and each reads as "-GROUP". Trusted, such a
// group would both reject a true release and be written back into the new name.
func nameGroup(name string, file parser.Release) string {
	g := file.Group
	if g == "" || reNumbering.MatchString(g) || tagWords[strings.ToLower(g)] {
		return ""
	}
	p := parser.Parse("x " + g)
	if p.Resolution != parser.ResUnknown || p.Source != parser.SourceUnknown || p.Codec != parser.CodecUnknown ||
		len(p.HDR) > 0 || len(p.Audio) > 0 || p.Streaming != "" || p.Edition != "" || p.Proper || p.Repack {
		return ""
	}
	// A "group" whose removal changes what else the name says was part of those words.
	base := strings.TrimSuffix(name, filepath.Ext(name))
	lower := strings.ToLower(base) // compared by byte offset, so only when lowering kept the length
	if i := strings.LastIndex(lower, "-"+strings.ToLower(g)); i >= 0 && len(lower) == len(base) && i+1+len(g) <= len(base) {
		without := parser.Parse(base[:i] + base[i+1+len(g):])
		if without.Source != file.Source || without.Resolution != file.Resolution ||
			without.Codec != file.Codec || len(without.Episodes) != len(file.Episodes) {
			return ""
		}
	}
	return g
}

// resolutionClass puts the SD resolutions together: a PAL DVD is 576p to the probe and may
// be "480p" in a release name, and Convert's facts call both "SD".
func resolutionClass(r string) string {
	switch r = strings.ToLower(strings.TrimSpace(r)); r {
	case "576p", "480p", "sd":
		return "sd"
	}
	return r
}

// overlayRelease is file with the facts release r states and the file's name doesn't laid
// over it: resolution, source, codec and group. Numbering, title and edition stay the file's.
func overlayRelease(file, r parser.Release) parser.Release {
	out := file
	if file.StatedResolution() == parser.ResUnknown && r.StatedResolution() != parser.ResUnknown {
		out.Resolution, out.ResolutionInferred = r.Resolution, false
	}
	if statedSource(file) == "" && statedSource(r) != "" {
		out.Source, out.SourceInferred = r.Source, false
	}
	if file.Codec == parser.CodecUnknown {
		out.Codec = r.Codec
	}
	if file.Group == "" {
		out.Group = r.Group
	}
	return out
}

// probedCodec reads a probed codec name the way a release name's codec token is read, so
// "hevc" and "x265" are one codec. Anything a release name couldn't say ("mpeg2video",
// Convert's "other") is unknown and never overrides a name.
func probedCodec(c string) parser.Codec {
	if c = strings.TrimSpace(c); c == "" {
		return parser.CodecUnknown
	}
	return parser.Parse("x " + c).Codec
}

// MovieRenameTarget is MovieTrackTarget for a file already in the library, named from
// src.Release() rather than from one name.
func (im *Importer) MovieRenameTarget(t MovieTrack, src RenameSource, ext string) string {
	return im.movieTrackTarget(t, src.Release(), ext)
}

// EpisodeRenameTarget is EpisodeTargetIn for an episode file already in the library,
// named from src.Release().
func (im *Importer) EpisodeRenameTarget(seriesFolder, title string, year, season, episode int, src RenameSource, ext string) string {
	return im.episodeTargetIn(seriesFolder, title, year, season, episode, src.Release(), ext, true)
}
