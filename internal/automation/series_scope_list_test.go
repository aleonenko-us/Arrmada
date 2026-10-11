package automation

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// A picked list wants exactly its own episodes that are aired and missing, monitored or
// not — like that many episode clicks.
func TestScopeWantedPickedList(t *testing.T) {
	got := scopeWanted(scopeShow(), SeriesScope{Season: 2, Episodes: []int{1, 2, 3, 5}})
	// E1 wanted, E2 unmonitored (picked, so wanted), E3 has a file, E5 hasn't aired.
	if !reflect.DeepEqual(got, []epKey{{2, 1}, {2, 2}}) {
		t.Errorf("picked list wants %v, want S02E01 and S02E02", got)
	}
}

func TestSeriesScopeValidateList(t *testing.T) {
	if err := (SeriesScope{Season: 0, Episodes: []int{1, 2}}).Validate(); err != nil {
		t.Errorf("a list of specials: %v", err)
	}
	long := make([]int, MaxScopeEpisodes+1)
	for i := range long {
		long[i] = i + 1
	}
	for _, sc := range []SeriesScope{
		{Season: 1, Episodes: long},
		{Season: -1, Episodes: []int{1}},
		{Season: 1, Episode: 2, Episodes: []int{1}},
		{Season: 1, Replace: true, Episodes: []int{1}},
		{Season: 1, Episodes: []int{0}},
	} {
		if sc.Validate() == nil {
			t.Errorf("%+v was accepted", sc)
		}
	}
}

// Searching five picked episodes is one search that grabs exactly those five, and leaves
// one stored attempt under the season.
func TestGrabForScopeEpisodeList(t *testing.T) {
	h := newStallHarness(t)
	repo := series.NewRepo(h.c.db)
	sr, err := repo.Create(h.ctx, series.Series{TMDBID: 9, Title: "Show", Year: 2020, Monitored: true, Status: "Ended"})
	if err != nil {
		t.Fatal(err)
	}
	var eps []series.Episode
	var offer []string
	for e := 1; e <= 12; e++ {
		eps = append(eps, series.Episode{SeasonNumber: 1, EpisodeNumber: e, AirDate: "2021-01-01", Monitored: true})
		offer = append(offer, "Show.S01E"+twoDigits(e)+".1080p.WEB-DL.x264-GRP")
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Monitored: true, Episodes: eps}}); err != nil {
		t.Fatal(err)
	}
	h.ix.offer(append(offer, "Show.S01.1080p.WEB-DL.x264-GRP")...)

	before := h.ix.searchCount()
	out, err := h.c.GrabForScope(WithSearchTrigger(context.Background(), TriggerManual), sr.ID, SeriesScope{Season: 1, Episodes: []int{2, 4, 6, 8, 10}, Trigger: "bulk"})
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), out.Grabbed...)
	sort.Strings(got)
	want := []string{"Show.S01E02.1080p.WEB-DL.x264-GRP", "Show.S01E04.1080p.WEB-DL.x264-GRP", "Show.S01E06.1080p.WEB-DL.x264-GRP", "Show.S01E08.1080p.WEB-DL.x264-GRP", "Show.S01E10.1080p.WEB-DL.x264-GRP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grabbed %v, want exactly the five picked episodes (5 of 12 is too few for the pack)", got)
	}
	if n := h.ix.searchCount() - before; n < 1 {
		t.Fatalf("searches = %d", n)
	}
	a := latestAttempt(t, h, AttemptSeries, sr.ID)
	if a.Scope != "S01" || a.Grabbed != 5 {
		t.Fatalf("attempt = %+v, want one under S01 with 5 grabbed", a)
	}
}

func twoDigits(n int) string {
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
