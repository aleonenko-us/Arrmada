package library

import (
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

// Release is the release a renamed file is named from — what an import names it from.
//
// An import names a file from its release, and the library name keeps only what the scheme
// of the day asked for: a film imported as "American Psycho (2000).mkv" has lost its
// release's quality, codec and group for good. A rename that read only the file name could
// never fill tokens added to the scheme later, so the rule is:
//
//   - The recorded release speaks first for every release fact a name uses: resolution,
//     source, codec, group and edition. The file's name fills in any it doesn't state, so a
//     release that doesn't parse leaves the name exactly as the file name alone would.
//   - A release that can't be about this file is ignored and the file name alone is used:
//     one stating a different resolution from the file's (probed, else named). The file was
//     replaced outside Arrmada and the record kept the old file's release.
//   - The probed codec wins over both names when it's a different codec. Convert re-encodes
//     files: it restamps the recorded release, but a file with no release keeps its old
//     codec in its name. When they agree, nothing changes.
//   - Season and episode numbers always come from the file's name, never the release: a
//     season pack's release names no episode.
//   - The title comes from neither. Names use the library's metadata title.
func (s RenameSource) Release() parser.Release {
	file := parser.Parse(s.FileName)
	rel := file
	if src := strings.TrimSpace(s.SourceRelease); src != "" {
		if r := parser.Parse(src); s.describes(r, file) {
			rel = overlayRelease(file, r)
		}
	}
	if c := probedCodec(s.Codec); c != parser.CodecUnknown && c != rel.Codec {
		rel.Codec = c
	}
	return rel
}

// describes reports whether release r can be about this file: it states no resolution, or
// the same class as the file has (read from the file when it was probed, else as named).
func (s RenameSource) describes(r, file parser.Release) bool {
	stated := r.StatedResolution()
	if stated == parser.ResUnknown {
		return true
	}
	have := s.Resolution
	if have == "" {
		have = string(file.StatedResolution())
	}
	return have == "" || resolutionClass(have) == resolutionClass(string(stated))
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

// overlayRelease is file with every naming fact release r states laid over it. Only the
// facts names are built from move across; numbering and the title stay the file's.
func overlayRelease(file, r parser.Release) parser.Release {
	out := file
	if r.StatedResolution() != parser.ResUnknown {
		out.Resolution, out.ResolutionInferred = r.Resolution, false
	}
	if statedSource(r) != "" {
		out.Source, out.SourceInferred = r.Source, false
	}
	if r.Codec != parser.CodecUnknown {
		out.Codec = r.Codec
	}
	if r.Group != "" {
		out.Group = r.Group
	}
	if r.Edition != "" {
		out.Edition = r.Edition
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

// MovieRenameTarget is MovieTrackTarget for a file already in the library: named from
// src.Release() — the recorded release first, then the file's name and probed codec —
// rather than from one name.
func (im *Importer) MovieRenameTarget(t MovieTrack, src RenameSource, ext string) string {
	return im.movieTrackTarget(t, src.Release(), ext)
}

// EpisodeRenameTarget is EpisodeTargetIn for an episode file already in the library:
// named from src.Release(), with the scheme's canonical season folder.
func (im *Importer) EpisodeRenameTarget(seriesFolder, title string, year, season, episode int, src RenameSource, ext string) string {
	return im.episodeTargetIn(seriesFolder, title, year, season, episode, src.Release(), ext, true)
}
