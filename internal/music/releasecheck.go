package music

import (
	"regexp"
	"strings"
)

// WhyNotForAlbum says, in words, why a release name isn't this album: "" when it is
// (ReleaseIsForAlbum). Interactive search shows it beside each release it turns down.
func WhyNotForAlbum(releaseName, artist, album string) string {
	switch {
	case ParseRelease(releaseName).Discography:
		return "a discography, not this one album"
	case !artistNamed(releaseName, artist):
		return "doesn't name " + artist
	case !containsWords(releaseName, album):
		return "doesn't name this album"
	}
	return ""
}

// reEditionHint is what a release name says about which edition it holds.
var reEditionHint = regexp.MustCompile(`(?i)\b(deluxe|expanded|anniversary|remaster(?:ed)?|special edition|limited edition|collector'?s edition|box ?set|bonus tracks?|japan(?:ese)? edition|vinyl|web|\d\s?cd|\d\s?discs?|sacd|dvd-?a)\b`)

// EditionHints are the edition words in a release name ("Deluxe", "2CD", "Vinyl"), for
// interactive search: two releases of one album are often different editions, and the
// name is the only clue before the download.
func EditionHints(releaseName string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range reEditionHint.FindAllString(releaseName, -1) {
		w := strings.ToUpper(strings.ReplaceAll(m, " ", ""))
		switch {
		case strings.HasSuffix(w, "CD"), strings.HasSuffix(w, "DISC"), strings.HasSuffix(w, "DISCS"),
			w == "SACD", strings.HasPrefix(w, "DVD"), w == "WEB":
			// Kept as the abbreviation it is: "2CD", "SACD", "WEB".
		default:
			w = strings.ToUpper(m[:1]) + strings.ToLower(m[1:])
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
