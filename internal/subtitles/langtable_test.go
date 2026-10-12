package subtitles

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Sidecar coverage reads tags through the shared language table: a Danish sidecar is
// Danish (it used to be unknown and so credited to English), regional tags count for
// their region, and a tag that is no language counts for nothing.
func TestPresentLanguagesSharedTable(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		wanted  []string
		present []string
	}{
		{"da sidecar does not cover English", []string{"Movie.mkv", "Movie.da.srt"}, []string{"en", "da"}, []string{"da"}},
		{"da sidecar alone leaves English missing", []string{"Movie.mkv", "Movie.da.srt"}, []string{"en"}, nil},
		{"pt-br sidecar covers pt-br", []string{"Movie.mkv", "Movie.pt-br.srt"}, []string{"en", "pt-br"}, []string{"pt-br"}},
		{"pt-BR spelling covers pt-br", []string{"Movie.mkv", "Movie.pt-BR.srt"}, []string{"pt-br"}, []string{"pt-br"}},
		{"pt-br sidecar covers a plain pt want", []string{"Movie.mkv", "Movie.pt-br.srt"}, []string{"pt"}, []string{"pt"}},
		{"plain pt sidecar is not pt-br", []string{"Movie.mkv", "Movie.pt.srt"}, []string{"pt-br"}, nil},
		{"zh-tw sidecar covers zh-tw, not zh-cn", []string{"Movie.mkv", "Movie.zh-tw.srt"}, []string{"zh-cn", "zh-tw"}, []string{"zh-tw"}},
		{"zh-Hant spelling covers zh-tw", []string{"Movie.mkv", "Movie.zh-Hant.srt"}, []string{"zh-tw"}, []string{"zh-tw"}},
		{"fre sidecar covers fr", []string{"Movie.mkv", "Movie.fre.srt"}, []string{"fr"}, []string{"fr"}},
		{"non-language tag covers nothing", []string{"Movie.mkv", "Movie.proper.srt"}, []string{"en"}, nil},
		{"bare sidecar still covers the first wanted", []string{"Movie.mkv", "Movie.srt"}, []string{"en"}, []string{"en"}},
		{"da.hi is Danish SDH", []string{"Movie.mkv", "Movie.da.hi.srt"}, []string{"da", "hi"}, []string{"da"}},
	}
	for _, c := range cases {
		_, video := mkFiles(t, c.files...)
		got := scanSidecars(video, c.wanted, "movie").Present
		if !reflect.DeepEqual(nonNil(got), nonNil(c.present)) {
			t.Errorf("%s: present = %v, want %v", c.name, got, c.present)
		}
	}
	// A non-language tag keeps its spelling in the variant list rather than looking untagged.
	_, video := mkFiles(t, "Movie.mkv", "Movie.proper.srt")
	if v := presentVariants(video); len(v) != 1 || v[0].Lang != "proper" {
		t.Errorf("variants = %+v, want the raw tag kept", v)
	}
}

// Embedded tracks match in every spelling a muxer writes: MP4 uses the 639-2/T codes
// (fra, deu, zho, nld) the old table didn't know.
func TestPickFullTrackSharedTable(t *testing.T) {
	for want, tag := range map[string]string{"fr": "fra", "de": "deu", "zh": "zho", "nl": "nld", "da": "dan", "sv": "swe"} {
		subs := []SubTrack{{Index: 0, Codec: "subrip", Lang: "eng", Text: true}, {Index: 1, Codec: "mov_text", Lang: tag, Text: true}}
		if got, ok := pickFullTrack(subs, want); !ok || got.Index != 1 {
			t.Errorf("want %s, track %s: got %+v, %v", want, tag, got, ok)
		}
	}
	// A regional want takes a base-tagged track only when no track carries the region,
	// and never one tagged for another region.
	subs := []SubTrack{
		{Index: 0, Codec: "subrip", Lang: "pt-pt", Text: true},
		{Index: 1, Codec: "subrip", Lang: "por", Text: true},
	}
	if got, ok := pickFullTrack(subs, "pt-br"); !ok || got.Index != 1 {
		t.Errorf("pt-br want: got %+v, %v; want the untagged-region por track", got, ok)
	}
	subs = append(subs, SubTrack{Index: 2, Codec: "subrip", Lang: "pt-BR", Text: true})
	if got, ok := pickFullTrack(subs, "pt-br"); !ok || got.Index != 2 {
		t.Errorf("pt-br want: got %+v, %v; want the pt-BR track", got, ok)
	}
	if _, ok := pickFullTrack(subs[:1], "pt-br"); ok {
		t.Error("a pt-pt track must not be taken for pt-br")
	}
}

func TestSetSettingsRejectsUnknownLanguage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &Service{settings: settings.NewService(st.DB())}
	ctx := context.Background()

	if err := s.SetSettings(ctx, nil, nil, []string{"en", "klingon"}); !errors.Is(err, ErrUnknownLanguage) {
		t.Fatalf("err = %v, want ErrUnknownLanguage", err)
	}
	if got := s.languages(ctx); !reflect.DeepEqual(got, []string{"en"}) {
		t.Errorf("a rejected list must save nothing; languages = %v", got)
	}
	auto := false
	if err := s.SetSettings(ctx, &auto, nil, []string{"xx"}); !errors.Is(err, ErrUnknownLanguage) {
		t.Fatalf("err = %v, want ErrUnknownLanguage", err)
	}
	if !s.settings.GetBool(ctx, keyMoviesAuto, true) {
		t.Error("a rejected request must not save its other fields either")
	}
	// Known codes are stored canonically, in order, without repeats.
	if err := s.SetSettings(ctx, nil, nil, []string{"ENG", "pt_BR", "fre", "en", " "}); err != nil {
		t.Fatal(err)
	}
	if got := s.languages(ctx); !reflect.DeepEqual(got, []string{"en", "pt-br", "fr"}) {
		t.Errorf("languages = %v, want [en pt-br fr]", got)
	}
	// A value saved before the table existed reads back canonical.
	if err := s.settings.Set(ctx, keyLanguages, "eng,fre,eng"); err != nil {
		t.Fatal(err)
	}
	if got := s.languages(ctx); !reflect.DeepEqual(got, []string{"en", "fr"}) {
		t.Errorf("legacy languages = %v, want [en fr]", got)
	}
}

// OpenSubtitles is searched with its own codes: pt-br as pt-br, plain Portuguese as
// pt-pt, Traditional Chinese as zh-tw.
func TestSearchSendsOpenSubtitlesLanguageCode(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	o := NewOpenSubtitles("key", "", "")
	o.baseURL = srv.URL
	for want, send := range map[string]string{"pt-br": "pt-br", "pt": "pt-pt", "zh-tw": "zh-tw", "fre": "fr", "en": "en"} {
		if _, err := o.Search(context.Background(), SearchRequest{IMDBID: "tt1", Language: want}); err != nil {
			t.Fatal(err)
		}
		if got.Get("languages") != send {
			t.Errorf("want %s: languages=%q, sent %q", want, send, got.Get("languages"))
		}
	}
}

// A pt-br subtitle is written as <base>.pt-br.srt and read back as pt-br coverage.
func TestSidecarPathRegional(t *testing.T) {
	_, video := mkFiles(t, "Movie.mkv")
	p := sidecarPath(video, "pt-BR")
	if want := "Movie.pt-br.srt"; filepath.Base(p) != want {
		t.Fatalf("sidecarPath = %s, want %s", p, want)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := scanSidecars(video, []string{"pt-br"}, "movie").Present; !reflect.DeepEqual(got, []string{"pt-br"}) {
		t.Errorf("present = %v, want [pt-br]", got)
	}
}
