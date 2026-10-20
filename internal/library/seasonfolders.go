package library

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The season folder names people (and other tools) store episodes under, in the
// languages Arrmada's users actually run: "Season 1", "S01", "Stagione 1", "Saison 01",
// "Staffel 1", "Temporada 1", "Series 1", "Season.01", "Seizoen 1", "Sezon 1", "Säsong 1",
// "Kausi 1", "Sæson 1", "Sesong 1", "Сезон 1" — any padding, a space, dot, underscore or
// dash between, and an optional bracketed tail ("Season 1 (2019)", "S01 [1080p]").
//
// A bare number is NOT a season folder: shows are called "1992", "24" and "1883", and a
// folder named like that is the show. Neither is anything with words around the number
// ("Season 4 Show") — only the whole name counts.
var reSeasonWord = regexp.MustCompile(`(?i)^(?:season|saison|stagione|staffel|temporada|series|seizoen|sezon|säsong|sasong|kausi|sæson|saeson|sesong|сезон)[ ._-]*0*(\d{1,4})(?:[ ._-]*[(\[][^()\[\]]*[)\]])?$`)

// reSeasonShort is "S1" / "S01" / "s001" (with the same optional bracketed tail).
var reSeasonShort = regexp.MustCompile(`(?i)^s[ ._-]?0*(\d{1,4})(?:[ ._-]*[(\[][^()\[\]]*[)\]])?$`)

// reSpecials is the specials folder ("Season 0" is read by the patterns above).
var reSpecials = regexp.MustCompile(`(?i)^(?:specials?|special episodes|speciali|spéciaux|speciaux|especiales|specials?[ ._-]*season)$`)

// reExtras is a show's bonus-material folder: it sits next to the season folders, inside
// the show's folder, so it is never the show itself.
var reExtras = regexp.MustCompile(`(?i)^(?:extras?|featurettes|behind the scenes|bonus|trailers|other|interviews|deleted scenes)$`)

// rePlexExtras is a Plex local-extras folder: featurettes, trailers and the like that sit
// inside a show's folder and are never episodes, whatever their files are called.
var rePlexExtras = regexp.MustCompile(`(?i)^(?:extras|featurettes|behind the scenes|deleted scenes|interviews|scenes|shorts|trailers|other)$`)

// IsExtrasFolder reports whether a folder name is a Plex local-extras folder.
func IsExtrasFolder(name string) bool { return rePlexExtras.MatchString(strings.TrimSpace(name)) }

// InExtrasFolder reports whether path sits in a Plex local-extras folder anywhere below
// showDir (Extras, Featurettes, Season 1/Deleted Scenes…).
func InExtrasFolder(showDir, path string) bool {
	rel, err := filepath.Rel(showDir, filepath.Dir(path))
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if IsExtrasFolder(part) {
			return true
		}
	}
	return false
}

// LinkMayHoldVideo reports whether the symlink at path leads to a folder or a video, or
// can't be followed (so nothing tells what it is): a link that may bring another show's
// files into a folder. A link to a poster or a subtitle doesn't.
func LinkMayHoldVideo(path string) bool {
	if isVideo(path) {
		return true
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return true
	}
	fi, err := os.Stat(target)
	return err != nil || fi.IsDir() || isVideo(target)
}

// SeasonFolderNumber reads the season number from a season folder's name ("Specials" is
// season 0). ok is false for anything that isn't a season folder — including a bare
// number, which is far more often a show called "1992" than a season.
func SeasonFolderNumber(name string) (int, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false
	}
	if reSpecials.MatchString(name) {
		return 0, true
	}
	for _, re := range []*regexp.Regexp{reSeasonWord, reSeasonShort} {
		if m := re.FindStringSubmatch(name); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// LooksLikeSeasonFolder reports whether a folder name is one that lives INSIDE a show's
// folder — a season folder, Specials or Extras — and so can never be a show's own folder.
func LooksLikeSeasonFolder(name string) bool {
	if _, ok := SeasonFolderNumber(name); ok {
		return true
	}
	return reExtras.MatchString(strings.TrimSpace(name))
}

// seasonTemplateRe turns a season folder template ("{title} Season {season}") into a
// pattern matching the names it renders, with the season number as the first group. nil
// when the template doesn't name the season at all.
func seasonTemplateRe(format string) *regexp.Regexp {
	if !strings.Contains(format, "{season}") && !strings.Contains(format, "{season00}") {
		return nil
	}
	// Render with stand-ins that survive the name cleaner, then turn each back into a
	// pattern.
	const seasonMark, titleMark, yearMark = "7351739", "QxTITLExQ", "8462846"
	rendered := renderName(format, map[string]string{
		"season": seasonMark, "season00": seasonMark, "title": titleMark, "year": yearMark,
	})
	if !strings.Contains(rendered, seasonMark) {
		return nil
	}
	pat := regexp.QuoteMeta(rendered)
	pat = strings.Replace(pat, seasonMark, `0*(\d{1,4})`, 1)
	pat = strings.ReplaceAll(pat, seasonMark, `\d{1,4}`)
	pat = strings.ReplaceAll(pat, titleMark, `.+`)
	pat = strings.ReplaceAll(pat, yearMark, `\d{4}`)
	re, err := regexp.Compile(`(?i)^` + pat + `$`)
	if err != nil {
		return nil
	}
	return re
}

// seasonNumberOf reads a season folder's number by the common names, then by the
// configured season folder template — so a library named "Stagione {season}" or
// "{title} - Season {season00}" by Arrmada itself is recognised as season folders too.
func (im *Importer) seasonNumberOf(name string) (int, bool) {
	if n, ok := SeasonFolderNumber(name); ok {
		return n, true
	}
	if im == nil {
		return 0, false
	}
	if re := seasonTemplateRe(im.seriesNamingScheme().SeasonFolder); re != nil {
		if m := re.FindStringSubmatch(strings.TrimSpace(name)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// IsSeasonFolderName reports whether name is a season folder — by the common names
// (SeasonFolderNumber, Specials included) or the configured season folder template — and
// so never a show's own folder. Extras folders aren't counted: "Extras" is also a show.
func (im *Importer) IsSeasonFolderName(name string) bool {
	if _, ok := SeasonFolderNumber(name); ok {
		return true
	}
	// A template of just "{season}" names season folders "1", "2"… but a bare number
	// is still read as a show ("1992", "24") when it decides a show's folder.
	if strings.Trim(strings.TrimSpace(name), "0123456789") == "" {
		return false
	}
	// Nor is a year: under a "{title} - {season}" template, "Show - 2005" is a show.
	n, ok := im.seasonNumberOf(name)
	return ok && n < 1900
}
