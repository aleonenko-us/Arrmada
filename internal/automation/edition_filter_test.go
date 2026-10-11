package automation

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// MOV-13: a track's edition is a requirement, the default track keeps off other tracks'
// editions, and no track takes the release another track's file came from.

func names(cands []quality.Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Name
	}
	return out
}

func TestEditionFilter(t *testing.T) {
	theatrical := "Blade.Runner.1982.2160p.UHD.BluRay.x265-GRP"
	dc := "Blade.Runner.1982.Directors.Cut.2160p.UHD.BluRay.x265-GRP"
	dcApos := "Blade Runner 1982 Director's Cut 1080p BluRay x264-OTHER"
	cands := []quality.Candidate{
		quality.NewCandidate(theatrical, 40, 10),
		quality.NewCandidate(dc, 40, 10),
		quality.NewCandidate(dcApos, 10, 10),
	}
	def := movies.Version{ID: 0, IsDefault: true, Label: "Default", Monitored: true}
	dcTrack := movies.Version{ID: 4, Label: "Director's Cut", Edition: "Director's Cut", Monitored: true}

	cases := []struct {
		name     string
		v        movies.Version
		siblings []movies.Version
		want     []string
	}{
		{"DC track keeps only DC (either spelling)", dcTrack, []movies.Version{def, dcTrack}, []string{dc, dcApos}},
		{"default track with a DC sibling drops DC", def, []movies.Version{def, dcTrack}, []string{theatrical}},
		{"movie without editions is unchanged", def, []movies.Version{def}, []string{theatrical, dc, dcApos}},
		// An unmonitored DC track doesn't keep the default off DC releases.
		{"unmonitored sibling", def, []movies.Version{def, {ID: 4, Edition: "Director's Cut"}}, []string{theatrical, dc, dcApos}},
		// Typed by hand as "directors cut": still the same edition.
		{"hand-typed edition", movies.Version{ID: 5, Edition: "directors cut"}, []movies.Version{def, {ID: 5, Edition: "directors cut"}}, []string{dc, dcApos}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := names(editionFilter(nil, cands, tc.v, tc.siblings))
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestExcludesSiblingSourceRelease(t *testing.T) {
	have := "Dune.2021.2160p.UHD.BluRay.x265-GRP"
	other := "Dune.2021.2160p.WEB-DL.DDP5.1.HDR.HEVC-OTHER"
	cands := []quality.Candidate{quality.NewCandidate(have, 40, 10), quality.NewCandidate(other, 20, 10)}
	def := movies.Version{ID: 0, IsDefault: true, Monitored: true, HasFile: true, SourceRelease: "Dune 2021 2160p UHD BluRay x265-GRP"}
	extra := movies.Version{ID: 3, Label: "HDR", Monitored: true}
	got := names(editionFilter(nil, cands, extra, []movies.Version{def, extra}))
	if len(got) != 1 || got[0] != other {
		t.Fatalf("got %v, want only %q (the default's own release fills one track only)", got, other)
	}
	// The track's own release isn't excluded from its own search (an upgrade compares to it).
	got = names(editionFilter(nil, cands, def, []movies.Version{def, extra}))
	if len(got) != 2 {
		t.Fatalf("default track's own source excluded: %v", got)
	}
}

// Two 4K tracks, one a Director's Cut; the indexer offers a theatrical and a DC 2160p.
// Each track gets its own release.
func TestGrabMissingEditionTracks(t *testing.T) {
	h := newStallHarness(t)
	id := h.addMovie(t, 78, "Blade Runner", 1982)
	if _, err := h.c.movies.AddVersion(h.ctx, id, "Director's Cut", "", "Directors Cut", true); err != nil {
		t.Fatal(err)
	}
	m, err := h.c.movies.Get(h.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	theatrical := "Blade.Runner.1982.2160p.UHD.BluRay.x265-GRP"
	dc := "Blade.Runner.1982.Directors.Cut.2160p.UHD.BluRay.x265-GRP"
	byName := map[string]indexer.Release{
		theatrical: {Title: theatrical, Indexer: "Fake", DownloadURL: magnetFor(theatrical)},
		dc:         {Title: dc, Indexer: "Fake", DownloadURL: magnetFor(dc)},
	}
	// The DC release is bigger and better-seeded, so without the filter it would win both.
	cands := []quality.Candidate{quality.NewCandidate(theatrical, 30, 10), quality.NewCandidate(dc, 40, 50)}
	want := h.c.missingVersions(h.ctx, id)
	if len(want) != 2 {
		t.Fatalf("missing tracks = %d, want 2", len(want))
	}
	if n := h.c.grabMissing(h.ctx, m, want, byName, cands); n != 2 {
		t.Fatalf("grabbed %d, want 2", n)
	}
	rows, err := h.c.db.Query(`SELECT version_id, title FROM grabs WHERE movie_id = ? ORDER BY version_id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int64]string{}
	for rows.Next() {
		var vid int64
		var title string
		if err := rows.Scan(&vid, &title); err != nil {
			t.Fatal(err)
		}
		got[vid] = title
	}
	if got[0] != theatrical {
		t.Errorf("default track grabbed %q, want the theatrical release", got[0])
	}
	var dcGrab string
	for vid, title := range got {
		if vid != 0 {
			dcGrab = title
		}
	}
	if dcGrab != dc {
		t.Errorf("DC track grabbed %q, want the DC release", dcGrab)
	}
}

// No DC release on offer: the DC track grabs nothing and stays wanted, and the default
// track still takes the theatrical release.
func TestGrabMissingNoEditionStaysWanted(t *testing.T) {
	h := newStallHarness(t)
	id := h.addMovie(t, 79, "Aliens", 1986)
	if _, err := h.c.movies.AddVersion(h.ctx, id, "Special", "", "Director's Cut", true); err != nil {
		t.Fatal(err)
	}
	m, _ := h.c.movies.Get(h.ctx, id)
	theatrical := "Aliens.1986.1080p.BluRay.x264-GRP"
	byName := map[string]indexer.Release{theatrical: {Title: theatrical, Indexer: "Fake", DownloadURL: magnetFor(theatrical)}}
	cands := []quality.Candidate{quality.NewCandidate(theatrical, 10, 10)}
	if n := h.c.grabMissing(h.ctx, m, h.c.missingVersions(h.ctx, id), byName, cands); n != 1 {
		t.Fatalf("grabbed %d, want 1 (the theatrical, for the default track)", n)
	}
	var extraGrabs int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM grabs WHERE movie_id = ? AND version_id != 0`, id).Scan(&extraGrabs); err != nil {
		t.Fatal(err)
	}
	if extraGrabs != 0 {
		t.Fatalf("the edition track grabbed a release without its edition")
	}
}

// An upgrade for the DC track is only ever a DC release: a better theatrical copy isn't an
// upgrade of it.
func TestUpgradePicksKeepEdition(t *testing.T) {
	h := newStallHarness(t)
	id := h.addMovie(t, 81, "Alien", 1979)
	p, err := h.c.quality.Create(h.ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "Up", UpgradesEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(p.ID, 10)
	v, err := h.c.movies.AddVersion(h.ctx, id, "DC", ref, "Director's Cut", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.db.Exec(`UPDATE movie_versions SET has_file = 1, file_path = '/x/Alien DC 720p.mkv', source_release = 'Alien.1979.Directors.Cut.720p.BluRay.x264-OLD' WHERE id = ?`, v.ID); err != nil {
		t.Fatal(err)
	}
	m, _ := h.c.movies.Get(h.ctx, id)
	rows, _ := h.c.movies.VersionRows(h.ctx, id)
	theatrical := quality.NewCandidate("Alien.1979.2160p.UHD.BluRay.x265-GRP", 40, 50)
	dc := quality.NewCandidate("Alien.1979.Directors.Cut.1080p.BluRay.x264-GRP", 12, 20)

	picks := h.c.movieUpgradePicks(h.ctx, m, rows[1:], rows, []quality.Candidate{theatrical})
	if len(picks) != 0 {
		t.Fatalf("the DC track would upgrade to %q", picks[0].pick.Name)
	}
	picks = h.c.movieUpgradePicks(h.ctx, m, rows[1:], rows, []quality.Candidate{theatrical, dc})
	if len(picks) != 1 || picks[0].pick.Name != dc.Name {
		t.Fatalf("picks = %+v, want the DC 1080p", picks)
	}
}

// Ranking for a track uses that track's profile and marks the releases it can't take as
// not eligible, saying why.
func TestRankReleasesForVersionUsesTrackProfile(t *testing.T) {
	h := newStallHarness(t)
	id := h.addMovie(t, 80, "Blade Runner", 1982)
	p, err := h.c.quality.Create(h.ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "1080 only", AllowedResolutions: []string{"1080p"}})
	if err != nil {
		t.Fatal(err)
	}
	only1080 := "custom:" + strconv.FormatInt(p.ID, 10)
	v, err := h.c.movies.AddVersion(h.ctx, id, "Director's Cut", only1080, "Director's Cut", true)
	if err != nil {
		t.Fatal(err)
	}
	theatrical := "Blade.Runner.1982.1080p.BluRay.x264-GRP"
	dc4k := "Blade.Runner.1982.Directors.Cut.2160p.UHD.BluRay.x265-GRP"
	dc1080 := "Blade.Runner.1982.Directors.Cut.1080p.BluRay.x264-GRP"
	h.ix.offer(theatrical, dc4k, dc1080)

	list, err := h.c.RankReleasesFor(h.ctx, id, v.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.Profile != only1080 {
		t.Errorf("ranked under %q, want the track's profile %q", list.Profile, only1080)
	}
	rows := map[string]RankedRelease{}
	for _, r := range list.Releases {
		rows[r.Title] = r
	}
	if r := rows[theatrical]; r.Eligible || !strings.Contains(r.RejectReason, "Director's Cut") {
		t.Errorf("theatrical row = %+v; want not eligible, naming the edition", r)
	}
	if r := rows[dc4k]; r.Eligible {
		t.Errorf("4K DC row eligible under a 1080p-only profile: %+v", r)
	}
	if r := rows[dc1080]; !r.Eligible || !r.Recommended {
		t.Errorf("1080p DC row = %+v; want the recommendation", r)
	}

	// The default track, ranked on its own, keeps off the DC releases.
	list, err = h.c.RankReleasesFor(h.ctx, id, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list.Releases {
		if r.Title != theatrical && r.Eligible {
			t.Errorf("default track: DC row eligible: %+v", r)
		}
	}

	// A track that isn't the movie's.
	if _, err := h.c.RankReleasesFor(h.ctx, id, v.ID+100, nil); !errors.Is(err, movies.ErrNotFound) {
		t.Errorf("foreign track: err = %v", err)
	}
}
