package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// overviewDiscovery answers the detail sheet and the trending row, recording the
// languages the trending overviews were asked in.
type overviewDiscovery struct {
	metadata.DiscoveryProvider
	mu    sync.Mutex
	asked []string
	// byLang answers TrendingOverviews per language; nil answers French for Amélie.
	byLang map[string]map[string]string
	// hang makes TrendingOverviews wait for its context, as a stuck TMDB would.
	hang bool
}

func (*overviewDiscovery) Available() bool { return true }

func (*overviewDiscovery) MediaDetails(context.Context, string, int) (*metadata.MediaDetail, error) {
	return &metadata.MediaDetail{MediaType: "movie", TMDBID: 194, Title: "Amélie", Year: 2001,
		Overview: "A shy waitress changes lives.",
		Overviews: []metadata.TranslatedOverview{
			{Lang: "fr", Country: "FR", Overview: "Amélie, une serveuse, change des vies."},
			{Lang: "ja", Country: "JP", Overview: "内気なウェイトレス。"},
		}}, nil
}

func (*overviewDiscovery) Trending(context.Context, string) ([]metadata.DiscoverItem, error) {
	return []metadata.DiscoverItem{
		{MediaType: "movie", TMDBID: 194, Title: "Amélie", Overview: "A shy waitress changes lives."},
		{MediaType: "series", TMDBID: 61068, Title: "Gomorrah", Overview: "Naples."},
	}, nil
}

func (o *overviewDiscovery) TrendingOverviews(ctx context.Context, _, lang string) (map[string]string, error) {
	o.mu.Lock()
	o.asked = append(o.asked, lang)
	o.mu.Unlock()
	if o.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if o.byLang != nil {
		return o.byLang[lang], nil
	}
	return map[string]string{"movie:194": "Amélie, une serveuse, change des vies."}, nil
}

// trendingCards is the trending handler's cards and how long it took.
func trendingCards(t *testing.T, a *api) ([]discoverCard, time.Duration) {
	t.Helper()
	rec := httptest.NewRecorder()
	start := time.Now()
	a.handleDiscoverTrending(rec, httptest.NewRequest("GET", "/api/v1/discover/trending?media=all", nil))
	took := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("trending: HTTP %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Items []discoverCard `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Items, took
}

// A stuck TMDB never holds the hero: after a short wait the cards come with their
// English overviews.
func TestTrendingOverviewsDontHoldTheHero(t *testing.T) {
	a, disc := overviewAPI(t)
	disc.hang = true
	setDisplay(t, a, "language", "fr")
	cards, took := trendingCards(t, a)
	if took > trendingOverviewWait+time.Second {
		t.Errorf("the hero waited %v for overviews, want about %v", took, trendingOverviewWait)
	}
	if len(cards) != 2 || cards[0].DisplayOverview != "" || cards[0].Overview != "A shy waitress changes lives." {
		t.Errorf("cards = %+v, want the English overviews", cards)
	}
}

// A regional choice (fr-CA) takes its own list's overviews and fills the cards it leaves
// without one from the base language's (fr), as the detail sheet does; a plain choice
// (fr) reads once.
func TestTrendingOverviewsRegionalFallback(t *testing.T) {
	a, disc := overviewAPI(t)
	disc.byLang = map[string]map[string]string{
		"fr-CA": {"movie:194": "Amélie, au Québec."},
		"fr":    {"movie:194": "Amélie, en France.", "series:61068": "Naples, en France."},
	}
	setDisplay(t, a, "language", "fr-ca")
	cards, _ := trendingCards(t, a)
	got := map[int]string{}
	for _, c := range cards {
		got[c.TMDBID] = c.DisplayOverview
	}
	if got[194] != "Amélie, au Québec." || got[61068] != "Naples, en France." {
		t.Errorf("fr-CA cards = %v, want Canada's first and France's for the rest", got)
	}
	if len(disc.asked) != 2 || disc.asked[0] != "fr-CA" || disc.asked[1] != "fr" {
		t.Errorf("asked %v, want [fr-CA fr]", disc.asked)
	}

	disc.asked = nil
	setDisplay(t, a, "language", "fr")
	trendingCards(t, a)
	if len(disc.asked) != 1 || disc.asked[0] != "fr" {
		t.Errorf("plain French asked %v, want one read", disc.asked)
	}
}

// overviewAPI is an api with stored settings, the Discovery above and an empty library.
func overviewAPI(t *testing.T) (*api, *overviewDiscovery) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	disc := &overviewDiscovery{}
	a := &api{deps: Deps{Settings: settings.NewService(st.DB()), Discovery: disc, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	enrichSnaps.Store(a, &enrichSnapEntry{at: time.Now().Add(time.Hour), snap: emptySnap()})
	t.Cleanup(func() { enrichSnaps.Delete(a) })
	return a, disc
}

func setDisplay(t *testing.T, a *api, mode, lang string) {
	t.Helper()
	ctx := context.Background()
	if err := a.deps.Settings.Set(ctx, settings.KeyDisplayTitleMode, mode); err != nil {
		t.Fatal(err)
	}
	if err := a.deps.Settings.Set(ctx, settings.KeyDisplayTitleLanguage, lang); err != nil {
		t.Fatal(err)
	}
}

// GitHub #35 on Discover: with titles shown in French the sheet describes a title in
// French from the record's own translations (and drops the rest before answering), the
// hero's trending cards carry French overviews read with language=fr, and the English
// overview stays each card's own; the default and original settings keep English and ask
// TMDB nothing more.
func TestDiscoverOverviewsFollowDisplayLanguage(t *testing.T) {
	a, disc := overviewAPI(t)

	detail := func() map[string]any {
		t.Helper()
		rec := fetchDetail(a, "movie", "194")
		if rec.Code != http.StatusOK {
			t.Fatalf("detail: HTTP %d %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), `"overviews"`) || strings.Contains(rec.Body.String(), "内気") {
			t.Errorf("the detail answer carries every language's overview: %s", rec.Body)
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}
	trending := func() []discoverCard {
		t.Helper()
		rec := httptest.NewRecorder()
		a.handleDiscoverTrending(rec, httptest.NewRequest("GET", "/api/v1/discover/trending?media=all", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("trending: HTTP %d %s", rec.Code, rec.Body)
		}
		var body struct {
			Items []discoverCard `json:"items"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Items
	}

	// Default: English everywhere, no extra TMDB request.
	if d := detail(); d["display_overview"] != nil || d["overview"] != "A shy waitress changes lives." {
		t.Errorf("default detail = %v / %v", d["overview"], d["display_overview"])
	}
	for _, c := range trending() {
		if c.DisplayOverview != "" {
			t.Errorf("default trending card %d shows %q", c.TMDBID, c.DisplayOverview)
		}
	}
	if len(disc.asked) != 0 {
		t.Errorf("the default setting read trending overviews in %v", disc.asked)
	}

	setDisplay(t, a, "language", "fr")
	d := detail()
	if d["display_overview"] != "Amélie, une serveuse, change des vies." || d["overview"] != "A shy waitress changes lives." {
		t.Errorf("French detail = %v / %v", d["overview"], d["display_overview"])
	}
	if card, _ := d["card"].(map[string]any); card["display_overview"] != "Amélie, une serveuse, change des vies." || card["overview"] != "A shy waitress changes lives." {
		t.Errorf("French detail card = %v", card)
	}
	cards := trending()
	if len(disc.asked) != 1 || disc.asked[0] != "fr" {
		t.Errorf("trending overviews asked in %v, want [fr]", disc.asked)
	}
	for _, c := range cards {
		switch c.TMDBID {
		case 194:
			if c.DisplayOverview != "Amélie, une serveuse, change des vies." || c.Overview != "A shy waitress changes lives." || c.Title != "Amélie" {
				t.Errorf("Amélie card = %+v", c)
			}
		case 61068:
			if c.DisplayOverview != "" {
				t.Errorf("Gomorrah has no French overview but shows %q", c.DisplayOverview)
			}
		}
	}

	// A language TMDB has no overview in: English.
	setDisplay(t, a, "language", "de")
	if d := detail(); d["display_overview"] != nil {
		t.Errorf("German detail shows %v, want the English overview", d["display_overview"])
	}
	// The original mode: English, even for a Japanese overview TMDB has.
	setDisplay(t, a, "original", "")
	if d := detail(); d["display_overview"] != nil {
		t.Errorf("original-mode detail shows %v", d["display_overview"])
	}
	before := len(disc.asked)
	trending()
	if len(disc.asked) != before {
		t.Error("the original mode read trending overviews")
	}
}

// The movie and series pages carry display_overview — the stored overview in the
// language titles are shown in — beside the English overview, which stays; English when
// none is stored and in every other mode.
func TestLibraryDisplayOverviewAPI(t *testing.T) {
	s, _, _ := displayServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	for _, q := range []string{
		`UPDATE movies SET overview = 'Actors tour Greece.', extra_json = json_set(extra_json, '$.display_names', json('{"original":"Ο Θίασος","original_language":"el","read":true,"langs":["fr"],"overview_lang":"fr","overviews":[{"lang":"fr","country":"FR","overview":"Des comédiens parcourent la Grèce."}]}')) WHERE id = 21`,
		`UPDATE movies SET overview = 'Paul goes to Arrakis.' WHERE id = 22`,
		`UPDATE series SET overview = 'Naples crime.', extra_json = json_set(extra_json, '$.display_names', json('{"original":"Gomorra - La serie","original_language":"it","read":true,"langs":["fr"],"overview_lang":"fr","overviews":[{"lang":"fr","overview":"Le crime à Naples."}]}')) WHERE id = 11`,
	} {
		if _, err := s.st.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	movie := func(id string) movies.Movie {
		t.Helper()
		rec := s.do("GET", "/api/v1/movies/"+id, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("movie %s: HTTP %d %s", id, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "display_names") {
			t.Errorf("movie %s answers the raw display names beside display_overview: %s", id, rec.Body)
		}
		return decodeDisplay[movies.Movie](t, rec.Body.Bytes())
	}
	show := func() series.Series {
		t.Helper()
		rec := s.do("GET", "/api/v1/series/11", admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("series: HTTP %d %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "display_names") {
			t.Errorf("the show answers the raw display names beside display_overview: %s", rec.Body)
		}
		return decodeDisplay[series.Series](t, rec.Body.Bytes())
	}
	if m := movie("21"); m.DisplayOverview != "" {
		t.Errorf("default: shows %q, want English", m.DisplayOverview)
	}

	if rec := s.doBody("PUT", "/api/v1/settings", `{"display_title_mode":"language","display_title_language":"fr"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("settings: HTTP %d %s", rec.Code, rec.Body)
	}
	if m := movie("21"); m.DisplayOverview != "Des comédiens parcourent la Grèce." || m.Overview != "Actors tour Greece." {
		t.Errorf("French film = %q / %q", m.Overview, m.DisplayOverview)
	}
	if m := movie("22"); m.DisplayOverview != "" || m.Overview != "Paul goes to Arrakis." {
		t.Errorf("no French overview stored: %q / %q, want English", m.Overview, m.DisplayOverview)
	}
	if sr := show(); sr.DisplayOverview != "Le crime à Naples." || sr.Overview != "Naples crime." {
		t.Errorf("French show = %q / %q", sr.Overview, sr.DisplayOverview)
	}

	if rec := s.doBody("PUT", "/api/v1/settings", `{"display_title_mode":"original"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("settings: HTTP %d %s", rec.Code, rec.Body)
	}
	if m := movie("21"); m.DisplayOverview != "" {
		t.Errorf("original mode shows %q, want English", m.DisplayOverview)
	}
	if sr := show(); sr.DisplayOverview != "" {
		t.Errorf("original mode show = %q, want English", sr.DisplayOverview)
	}
}
