package applog

import (
	"strings"
	"testing"
)

func TestRedactText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"GET https://t.example/api?t=search&apikey=abc123def&q=dune", "GET https://t.example/api?t=search&apikey=«redacted»&q=dune"},
		{"plex said 401 X-Plex-Token: Zq81XwYy", "plex said 401 X-Plex-Token: «redacted»"},
		{`body {"password":"hunter22","user":"x"}`, `body {"password":"«redacted»","user":"x"}`},
		{"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.x", "Authorization: Bearer «redacted»"},
		{"dial qbit http://admin:s3cretpw@qbit:8080/api", "dial qbit http://«redacted»@qbit:8080/api"},
		{"sent to reader_42@kindle.com ok", "sent to «email» ok"},
		{"password_login=true token=«redacted»", "password_login=true token=«redacted»"},
	}
	for _, c := range cases {
		if got := RedactText(c.in); got != c.want {
			t.Errorf("RedactText(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestRewriteAttrs(t *testing.T) {
	in := "media=book title=The Long Road by=Marigold Banks status=200"
	got := RewriteAttrs(in, func(k, v string) string {
		if k == "by" {
			return "«user»"
		}
		return v
	})
	if want := "media=book title=The Long Road by=«user» status=200"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := RewriteAttrs("no pairs here", func(k, v string) string { return "x" }); got != "no pairs here" {
		t.Errorf("no pairs: %q", got)
	}
}

// Names are matched as words, the way titles are matched everywhere else: case, dots and
// underscores don't hide a title, accents fold, and a longer name wins over its prefix.
func TestTermScrubber(t *testing.T) {
	s := NewTermScrubber()
	s.Add("Zephyrine Quillfeather Chronicles", "«name»")
	s.Add("Dune", "«name»")
	s.Add("Dune Messiah", "«name»")
	s.Add("Pokémon Heroes", "«name»")
	s.Add("Ender's Game", "«name»")
	s.Add("Love & Death", "«name»")
	s.Add("It", "«name»") // too short to replace safely
	s.Seal()

	cases := []struct{ in, want string }{
		{"book: imported title=Zephyrine Quillfeather Chronicles edition=audiobook", "book: imported title=«name» edition=audiobook"},
		{"grabbed Zephyrine.Quillfeather.Chronicles.2024.M4B", "grabbed «name».2024.M4B"},
		{"path=/audiobooks/ZEPHYRINE_QUILLFEATHER_CHRONICLES/01.mp3", "path=/audiobooks/«name»/01.mp3"},
		{"Dune Messiah and Dunes", "«name» and Dunes"},
		{"pokemon heroes (2003)", "«name» (2003)"},
		{"Ender's Game", "«name»"},
		{"Love and Death.mkv", "«name».mkv"},
		{"is it ok", "is it ok"},
		{"nothing to see", "nothing to see"},
	}
	for _, c := range cases {
		if got := s.Scrub(c.in); got != c.want {
			t.Errorf("Scrub(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	if !strings.Contains(NewTermScrubber().Scrub("x"), "x") {
		t.Error("an empty scrubber changes nothing")
	}
}
