package parser

import (
	"path/filepath"
	"strings"
)

// NameWords is what a release or file name says before its encode details start, as
// TitleWords: "Show.The.Christmas.Special.2019.1080p.WEB-GRP" gives
// [show the christmas special 2019]. A leading "[Group]" tag is dropped, and so is a
// trailing "-GROUP" on a name with no quality tags; other brackets only lose the brackets,
// since "(US)" or "(2019)" says which show it is.
//
// Parse's Title can't stand in for it: a name with no season marker and no year reads as
// one long title, encode tags included.
func NameWords(name string) []string {
	// Only a video extension: a release name's last dotted part is usually a tag
	// ("....1080p.WEB"), which filepath.Ext can't tell apart from one.
	for _, ext := range videoExts {
		if strings.EqualFold(filepath.Ext(name), ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	name = reAnimeGroup.ReplaceAllString(strings.ReplaceAll(name, "_", " "), "")
	if q := reEpisodeQualityStart.FindStringIndex(name); q != nil {
		name = name[:q[0]]
	} else {
		name = reGroup.ReplaceAllString(name, "")
	}
	return TitleWords(name)
}
