package libfolders

import "strings"

// Folder rules: what makes a new title go to an extra folder by itself.
const (
	RuleNone      = ""          // chosen by hand only
	RuleAnimation = "animation" // TMDB genre Animation
	RuleAnime     = "anime"     // a show with anime numbering; a Japanese animated film
	RuleKids      = "kids"      // a kids' rating (G, TV-Y, TV-Y7, TV-G) or TMDB's Kids genre for shows
)

// ValidRule reports whether r is a rule this build knows.
func ValidRule(r string) bool {
	switch r {
	case RuleNone, RuleAnimation, RuleAnime, RuleKids:
		return true
	}
	return false
}

// Facts is what the rules can look at, all from metadata Arrmada already stores for the
// title (movies: extra_json; series: extra_json and series_type).
type Facts struct {
	Genres []string // TMDB genre names ("Animation", "Family", "Kids")
	// OriginalLanguage is TMDB's original_language ("ja").
	OriginalLanguage string
	// Certification is the US rating: a film's certification ("G", "PG-13"), a show's
	// content rating ("TV-Y7", "TV-MA"). "" when TMDB has none.
	Certification string
	// Anime is a show's anime numbering (series_type 'anime': detected on add from
	// Japanese + Animation, or switched on by hand). Unused for films.
	Anime bool
}

// kidsRatings are the US ratings meant for children: the MPA's G and the TV Parental
// Guidelines' TV-Y, TV-Y7 (and its FV variant) and TV-G.
var kidsRatings = map[string]bool{"G": true, "TV-Y": true, "TV-Y7": true, "TV-Y7-FV": true, "TV-G": true}

// Matches reports whether a title with facts f meets rule for a folder of library media.
// RuleNone never matches: such a folder is only ever chosen by hand.
func Matches(rule, media string, f Facts) bool {
	switch rule {
	case RuleAnimation:
		return hasGenre(f.Genres, "Animation")
	case RuleAnime:
		if media == MediaSeries {
			return f.Anime
		}
		// Films have no anime flag: the same test series use when they're added
		// (Japanese original language and the Animation genre).
		return hasGenre(f.Genres, "Animation") && strings.EqualFold(strings.TrimSpace(f.OriginalLanguage), "ja")
	case RuleKids:
		if kidsRatings[strings.ToUpper(strings.TrimSpace(f.Certification))] {
			return true
		}
		// TMDB files children's TV under its own "Kids" genre (films have none).
		return media == MediaSeries && hasGenre(f.Genres, "Kids")
	}
	return false
}

func hasGenre(genres []string, want string) bool {
	for _, g := range genres {
		if strings.EqualFold(strings.TrimSpace(g), want) {
			return true
		}
	}
	return false
}
