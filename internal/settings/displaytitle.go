package settings

import "github.com/tristenlammi/arrmada/internal/displaytitle"

// Settings → Library → "Show titles as": which of a film's or show's names the app shows
// (internal/displaytitle). Presentation only — matching, naming and the API's title keep
// the canonical TMDB English title whatever these say.
const (
	// KeyDisplayTitleMode is english (the default), original or language.
	KeyDisplayTitleMode = "display_title_mode"
	// KeyDisplayTitleLanguage is the language mode's language, a canonical code.
	KeyDisplayTitleLanguage = "display_title_language"
	// KeyDisplayTitleOriginalIn is a comma list of canonical codes: a work originally in
	// one of them shows its original title whatever the mode.
	KeyDisplayTitleOriginalIn = "display_title_original_in"
)

// DisplayTitlePolicy reads the stored setting through get (a settings read).
func DisplayTitlePolicy(get func(key string) string) displaytitle.Policy {
	return displaytitle.ParsePolicy(get(KeyDisplayTitleMode), get(KeyDisplayTitleLanguage), get(KeyDisplayTitleOriginalIn))
}
