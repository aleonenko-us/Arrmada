package libfolders

import "testing"

func TestRulesMatch(t *testing.T) {
	animated := Facts{Genres: []string{"Family", "Animation"}}
	cases := []struct {
		name  string
		rule  string
		media string
		f     Facts
		want  bool
	}{
		{"no rule never matches", RuleNone, MediaMovie, animated, false},
		{"animation genre", RuleAnimation, MediaMovie, animated, true},
		{"animation genre, any case", RuleAnimation, MediaSeries, Facts{Genres: []string{" animation "}}, true},
		{"not animated", RuleAnimation, MediaMovie, Facts{Genres: []string{"Drama"}}, false},
		{"anime film: Japanese and animated", RuleAnime, MediaMovie, Facts{Genres: []string{"Animation"}, OriginalLanguage: "ja"}, true},
		{"animated but not Japanese is no anime film", RuleAnime, MediaMovie, Facts{Genres: []string{"Animation"}, OriginalLanguage: "en"}, false},
		{"Japanese live action is no anime film", RuleAnime, MediaMovie, Facts{Genres: []string{"Drama"}, OriginalLanguage: "ja"}, false},
		{"anime show: its anime flag", RuleAnime, MediaSeries, Facts{Anime: true}, true},
		// A show's anime flag is what decides, so one switched off by hand isn't anime even
		// when TMDB calls it Japanese animation.
		{"show with the flag off", RuleAnime, MediaSeries, Facts{Genres: []string{"Animation"}, OriginalLanguage: "ja"}, false},
		{"G film", RuleKids, MediaMovie, Facts{Certification: "G"}, true},
		{"PG film isn't kids", RuleKids, MediaMovie, Facts{Certification: "PG"}, false},
		{"TV-Y show", RuleKids, MediaSeries, Facts{Certification: "TV-Y"}, true},
		{"TV-Y7 show", RuleKids, MediaSeries, Facts{Certification: "tv-y7"}, true},
		{"TV-Y7-FV show", RuleKids, MediaSeries, Facts{Certification: "TV-Y7-FV"}, true},
		{"TV-G show", RuleKids, MediaSeries, Facts{Certification: "TV-G"}, true},
		{"TV-14 show isn't kids", RuleKids, MediaSeries, Facts{Certification: "TV-14"}, false},
		{"TMDB Kids genre for a show", RuleKids, MediaSeries, Facts{Genres: []string{"Kids"}}, true},
		{"films have no Kids genre", RuleKids, MediaMovie, Facts{Genres: []string{"Kids"}}, false},
		{"unknown rule", "romance", MediaMovie, animated, false},
	}
	for _, c := range cases {
		if got := Matches(c.rule, c.media, c.f); got != c.want {
			t.Errorf("%s: Matches(%q) = %v, want %v", c.name, c.rule, got, c.want)
		}
	}
	for _, r := range []string{RuleNone, RuleAnimation, RuleAnime, RuleKids} {
		if !ValidRule(r) {
			t.Errorf("%q should be a valid rule", r)
		}
	}
	if ValidRule("romance") {
		t.Error("an unknown rule passed")
	}
}
