package music

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

func TestPickEdition(t *testing.T) {
	e := func(id int64, date, country, format string, tracks int) Edition {
		return Edition{ID: id, MBID: fmt.Sprint("rel-", id), ReleaseDate: date, Country: country, MediaSummary: format,
			TrackCount: tracks, Status: "Official"}
	}
	cd97 := e(1, "1997-05-21", "GB", "CD", 12)
	us97 := e(2, "1997-07-01", "US", "CD", 12)
	vinyl97 := e(3, "1997-05-21", "GB", "12\" Vinyl", 12)
	digital97 := e(4, "1997", "XW", "Digital Media", 12)
	deluxe := e(5, "2009-03-24", "XE", "2×CD", 26)
	bootleg := e(6, "1996-01-01", "US", "CD", 30)
	bootleg.Status = "Bootleg"
	dvd := e(7, "1990-01-01", "GB", "DVD-Video", 12)
	legacy := Edition{ID: 8, Legacy: true, TrackCount: 12}
	all := []Edition{deluxe, us97, vinyl97, cd97, bootleg, dvd, legacy}

	cases := []struct {
		name   string
		eds    []Edition
		policy EditionPolicy
		want   int64
	}{
		{"original: the earliest, CD before vinyl on the same day", all, EditionPolicy{Mode: PolicyOriginal}, 1},
		{"original: a bare year ties with the day, digital wins the tie", append(all, digital97), EditionPolicy{Mode: PolicyOriginal}, 4},
		{"most complete: the most tracks (bootlegs aren't acceptable)", all, EditionPolicy{Mode: PolicyMostComplete}, 5},
		{"preferred: country order first", all, EditionPolicy{Mode: PolicyPreferred, Countries: []string{"US", "GB"}, Media: []string{"CD"}}, 2},
		{"preferred: media order breaks a country tie", []Edition{vinyl97, cd97}, EditionPolicy{Mode: PolicyPreferred, Countries: []string{"GB"}, Media: []string{"CD", "12\" Vinyl"}}, 1},
		{"unknown mode reads as original", all, EditionPolicy{Mode: "nonsense"}, 1},
	}
	for _, c := range cases {
		got, ok := PickEdition(c.eds, c.policy)
		if !ok || got.ID != c.want {
			t.Errorf("%s: picked %d (%s), want %d", c.name, got.ID, EditionLabel(got), c.want)
		}
	}
	if _, ok := PickEdition([]Edition{bootleg, dvd, legacy}, EditionPolicy{Mode: PolicyOriginal}); ok {
		t.Error("nothing acceptable: picked one anyway")
	}
}

func TestEditionAcceptable(t *testing.T) {
	cases := []struct {
		e      Edition
		pinned bool
		want   bool
	}{
		{Edition{Status: "Official", MediaSummary: "CD"}, false, true},
		{Edition{Status: "", MediaSummary: "Digital Media"}, false, true},
		{Edition{Status: "Bootleg", MediaSummary: "CD"}, false, false},
		{Edition{Status: "Bootleg", MediaSummary: "CD"}, true, true},
		{Edition{Status: "Promotion"}, false, false},
		{Edition{Status: "Pseudo-Release"}, false, false},
		{Edition{Status: "Official", MediaSummary: "DVD-Video"}, false, false},
		{Edition{Status: "Official", MediaSummary: "Blu-ray"}, false, false},
		{Edition{Status: "Official", MediaSummary: "2×CD + DVD-Video"}, false, true},
		{Edition{Status: "Official", Media: []Medium{{Format: "CD"}, {Format: "DVD"}}}, false, true},
	}
	for i, c := range cases {
		if got := EditionAcceptable(c.e, c.pinned); got != c.want {
			t.Errorf("case %d (%+v pinned=%v): %v, want %v", i, c.e, c.pinned, got, c.want)
		}
	}
}

func TestEditionsEquivalent(t *testing.T) {
	a := edition("rel-a", "US", "1997", "CD", []tr{{"Airbag", 284, ""}, {"Paranoid Android", 383, ""}})
	b := edition("rel-b", "GB", "1997", "CD", []tr{{"airbag", 286, ""}, {"Paranoid Android", 381, ""}})
	if !EditionsEquivalent(a, b) {
		t.Error("same tracks, lengths within 3 s: not equivalent")
	}
	c := edition("rel-c", "GB", "1997", "CD", []tr{{"Airbag", 288, ""}, {"Paranoid Android", 383, ""}})
	if EditionsEquivalent(a, c) {
		t.Error("a 4 s difference made them equivalent")
	}
	d := edition("rel-d", "GB", "1997", "CD", []tr{{"Paranoid Android", 383, ""}, {"Airbag", 284, ""}})
	if EditionsEquivalent(a, d) {
		t.Error("reordered tracks made them equivalent")
	}
	e := edition("rel-e", "GB", "1997", "CD", []tr{{"Airbag", 284, ""}, {"Paranoid Android", 383, ""}, {"Lull", 147, ""}})
	if EditionsEquivalent(a, e) {
		t.Error("a bonus track made them equivalent")
	}
	if EditionsEquivalent(a, Edition{TrackCount: 2}) {
		t.Error("an edition known only by its summary is equivalent to nothing")
	}
}

func TestMapTracks(t *testing.T) {
	from := []trackIdent{
		{ID: 1, RecordingID: 100, Disc: 1, Position: 1, Title: "Airbag"},
		{ID: 2, MBID: "rt-2", Disc: 1, Position: 2, Title: "Paranoid Android"},
		{ID: 3, Disc: 1, Position: 3, Title: "Subterranean Homesick Alien"},
		{ID: 4, Disc: 1, Position: 9, Title: "Lucky"},
		{ID: 5, Disc: 1, Position: 4, Title: "A Song Nowhere Else"},
	}
	to := []trackIdent{
		{ID: 11, RecordingID: 100, Disc: 2, Position: 7, Title: "Airbag (Remastered)"},
		{ID: 12, MBID: "rt-2", Disc: 1, Position: 2, Title: "Paranoid Android"},
		{ID: 13, Disc: 1, Position: 3, Title: "Subterranean Homesick Alien"},
		{ID: 14, Disc: 1, Position: 4, Title: "Exit Music"},
		{ID: 15, Disc: 1, Position: 11, Title: "Lucky"},
	}
	got := mapTracks(from, to)
	want := map[int64]int64{1: 11, 2: 12, 3: 13, 4: 15}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("mapped %v, want %v (a different song at the same position never maps)", got, want)
	}
}

// fakeEditions answers the editions calls from canned releases.
type fakeEditions struct {
	flakyProvider
	releases map[string][]metadata.ReleaseSummary // release group → releases
	details  map[string]metadata.ReleaseDetail
	calls    int
}

func (f *fakeEditions) AlbumTracks(context.Context, string) ([]metadata.TrackResult, error) {
	return nil, nil
}
func (f *fakeEditions) AlbumReleases(_ context.Context, rg string) ([]metadata.ReleaseSummary, error) {
	f.calls++
	return f.releases[rg], nil
}
func (f *fakeEditions) ReleaseTracks(_ context.Context, rel string) (metadata.ReleaseDetail, error) {
	f.calls++
	d, ok := f.details[rel]
	if !ok {
		return d, fmt.Errorf("no release %s", rel)
	}
	return d, nil
}

func mbRelease(mbid, date, country, format string, titles []string, recPrefix string, rtPrefix string) (metadata.ReleaseSummary, metadata.ReleaseDetail) {
	md := metadata.ReleaseMedium{Position: 1, Format: format, TrackCount: len(titles)}
	for i, t := range titles {
		md.Tracks = append(md.Tracks, metadata.ReleaseTrack{
			MBID: fmt.Sprintf("%s-%d", rtPrefix, i+1), RecordingMBID: fmt.Sprintf("%s-%d", recPrefix, i+1),
			Position: i + 1, Title: t, LengthMs: 200000 + i*1000,
		})
	}
	credit := []metadata.CreditPart{{ArtistMBID: "mb-rh", ArtistName: "Radiohead", Name: "Radiohead"}}
	sum := metadata.ReleaseSummary{MBID: mbid, Title: "OK Computer", Status: "Official", Date: date, Country: country,
		Credit: credit, Media: []metadata.ReleaseMedium{{Position: 1, Format: format, TrackCount: len(titles)}}}
	det := metadata.ReleaseDetail{ReleaseSummary: sum}
	det.Media = []metadata.ReleaseMedium{md}
	return sum, det
}

// MUS-30: a migrated album's legacy edition is re-keyed to a real MusicBrainz edition on
// its first look: every file row moves to the matching track, one that fits nothing stays
// on the legacy edition (still listed), and no file on disk is touched.
func TestRekeyLegacyEditionKeepsFiles(t *testing.T) {
	r, ctx := testRepo(t)
	dir := t.TempDir()
	a, err := r.CreateArtist(ctx, Artist{MBID: "mb-rh", Name: "Radiohead", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	albumID, err := r.UpsertAlbum(ctx, Album{ArtistID: a.ID, MBID: "rg-okc", Title: "OK Computer", Year: 1997, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	// The legacy edition, as the migration builds it: no MBID, the release-track MBIDs of
	// the release it was read from, no recording MBIDs, one unmonitored track.
	legacyID, err := r.AddEdition(ctx, albumID, EditionInput{
		Edition: Edition{Legacy: true, TracksFetched: true},
		Tracks: []EditionTrack{
			{Disc: 1, Position: 1, Title: "Airbag", MBID: "rt-std-1"},
			{Disc: 1, Position: 2, Title: "Paranoid Android", MBID: "rt-std-2", Unmonitored: true},
			{Disc: 1, Position: 3, Title: "Subterranean Homesick Alien"}, // no MBID: by position and title
			{Disc: 1, Position: 4, Title: "A Track MusicBrainz Lost"},    // fits nothing
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetTargetEdition(ctx, albumID, legacyID, false); err != nil {
		t.Fatal(err)
	}
	legacy, err := r.Edition(ctx, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	type onDisk struct {
		path string
		data []byte
	}
	var files []onDisk
	for i, tk := range legacy.Media[0].Tracks {
		p := filepath.Join(dir, fmt.Sprintf("%02d - %s.flac", i+1, tk.Title))
		data := []byte(fmt.Sprintf("audio %d", i))
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordTrackFile(ctx, tk.ID, FileRecord{Path: p, SizeBytes: int64(len(data)), Quality: QualityFLAC}); err != nil {
			t.Fatal(err)
		}
		files = append(files, onDisk{p, data})
	}

	std, stdDet := mbRelease("rel-std", "1997-05-21", "GB", "CD",
		[]string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien"}, "rec", "rt-std")
	vinyl, vinylDet := mbRelease("rel-vinyl", "1997-06-16", "GB", "12\" Vinyl",
		[]string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien"}, "rec", "rt-vinyl")
	fake := &fakeEditions{
		releases: map[string][]metadata.ReleaseSummary{"rg-okc": {vinyl, std}},
		details:  map[string]metadata.ReleaseDetail{"rel-std": stdDet, "rel-vinyl": vinylDet},
	}
	svc := &Service{repo: r, meta: fake, log: slog.Default()}
	al, err := r.GetAlbum(ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureTracks(ctx, al); err != nil {
		t.Fatal(err)
	}

	al, _ = r.GetAlbum(ctx, albumID)
	target, err := r.Edition(ctx, al.TargetEditionID)
	if err != nil {
		t.Fatal(err)
	}
	if target.MBID != "rel-std" {
		t.Fatalf("target is %q, want the original CD (the policy's pick)", target.MBID)
	}
	tracks, _ := r.TracksFor(ctx, albumID)
	if len(tracks) != 3 {
		t.Fatalf("target tracks = %d, want 3", len(tracks))
	}
	for i, tk := range tracks {
		if !tk.HasFile || tk.FilePath != files[i].path {
			t.Errorf("track %q: file %q, want %q", tk.Title, tk.FilePath, files[i].path)
		}
	}
	if tracks[1].Monitored {
		t.Error("the unmonitored track's choice was lost in the re-key")
	}
	all, _ := r.AlbumFiles(ctx, albumID)
	if len(all) != 4 {
		t.Fatalf("file rows = %d, want all 4 kept", len(all))
	}
	views, _, err := svc.AlbumEditions(ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	sawLegacy := false
	for _, v := range views {
		if v.Legacy {
			sawLegacy = v.Files == 1
		}
	}
	if !sawLegacy {
		t.Errorf("the legacy edition still holding a file must stay listed: %+v", views)
	}
	for _, f := range files {
		got, err := os.ReadFile(f.path)
		if err != nil || string(got) != string(f.data) {
			t.Errorf("%s changed on disk: %q, %v", f.path, got, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(files) {
		t.Errorf("the folder holds %d entries, want exactly the %d files", len(entries), len(files))
	}

	// Switching by hand to the vinyl and back loses no file row.
	if _, err := svc.SetEdition(ctx, albumID, mustEditionByMBID(t, r, albumID, "rel-vinyl"), true); err != nil {
		t.Fatal(err)
	}
	tracks, _ = r.TracksFor(ctx, albumID)
	for _, tk := range tracks {
		if !tk.HasFile {
			t.Errorf("after switching to the vinyl, %q has no file", tk.Title)
		}
	}
	if _, err := svc.SetEdition(ctx, albumID, target.ID, false); err != nil {
		t.Fatal(err)
	}
	if all, _ := r.AlbumFiles(ctx, albumID); len(all) != 4 {
		t.Errorf("after switching back: %d file rows, want 4", len(all))
	}
	tracks, _ = r.TracksFor(ctx, albumID)
	for i, tk := range tracks {
		if tk.FilePath != files[i].path {
			t.Errorf("after switching back, %q is at %q", tk.Title, tk.FilePath)
		}
	}
	// Listed once: a second look makes no MusicBrainz call.
	calls := fake.calls
	al, _ = r.GetAlbum(ctx, albumID)
	if err := svc.EnsureTracks(ctx, al); err != nil || fake.calls != calls {
		t.Errorf("a second look made %d calls (%v), want none", fake.calls-calls, err)
	}
}

func mustEditionByMBID(t *testing.T, r *Repo, albumID int64, mbid string) int64 {
	t.Helper()
	eds, err := r.Editions(context.Background(), albumID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eds {
		if e.MBID == mbid {
			return e.ID
		}
	}
	t.Fatalf("no edition %s", mbid)
	return 0
}

// A deluxe edition the matcher switched to, then switched back by hand to the standard:
// the bonus tracks' files stay on the deluxe's tracks (listed as the album's extras), and
// switching to the deluxe again finds them.
func TestSwitchEditionKeepsBonusFiles(t *testing.T) {
	r, ctx := testRepo(t)
	a, _ := r.CreateArtist(ctx, Artist{MBID: "mb-x", Name: "X", Monitored: true})
	albumID, _ := r.UpsertAlbum(ctx, Album{ArtistID: a.ID, MBID: "rg-x", Title: "X", Monitored: true})
	std, err := r.AddEdition(ctx, albumID, EditionInput{Edition: Edition{MBID: "x-std"}, Tracks: []EditionTrack{
		{Disc: 1, Position: 1, Title: "One", RecordingMBID: "r1"}, {Disc: 1, Position: 2, Title: "Two", RecordingMBID: "r2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	dlx, err := r.AddEdition(ctx, albumID, EditionInput{Edition: Edition{MBID: "x-dlx"}, Tracks: []EditionTrack{
		{Disc: 1, Position: 1, Title: "One", RecordingMBID: "r1"}, {Disc: 1, Position: 2, Title: "Two", RecordingMBID: "r2"},
		{Disc: 1, Position: 3, Title: "Bonus", RecordingMBID: "r3"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SwitchEdition(ctx, albumID, dlx, false); err != nil {
		t.Fatal(err)
	}
	tracks, _ := r.TracksFor(ctx, albumID)
	for i, tk := range tracks {
		if err := r.RecordTrackFile(ctx, tk.ID, FileRecord{Path: fmt.Sprintf("/m/x/%d.flac", i), Quality: QualityFLAC}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := r.SwitchEdition(ctx, albumID, std, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 2 || res.Kept != 1 {
		t.Errorf("to the standard: moved %d kept %d, want 2 and 1", res.Moved, res.Kept)
	}
	al, _ := r.GetAlbum(ctx, albumID)
	if al.HaveTracks != 2 || al.TrackCount != 2 || !al.Complete() {
		t.Errorf("the standard reads %d/%d, want complete", al.HaveTracks, al.TrackCount)
	}
	if _, err := r.SwitchEdition(ctx, albumID, dlx, false); err != nil {
		t.Fatal(err)
	}
	al, _ = r.GetAlbum(ctx, albumID)
	if al.HaveTracks != 3 {
		t.Errorf("back on the deluxe: %d/%d, want the bonus file found again", al.HaveTracks, al.TrackCount)
	}
}

// tr is one track of a fixture edition.
type tr struct {
	title string
	sec   int
	rec   string // recording MBID ("" none)
}

var nextFixtureID int64 = 1000

func fixtureID() int64 { nextFixtureID++; return nextFixtureID }

// edition builds an edition; discs are given as track lists, in order.
func edition(mbid, country, date, format string, discs ...[]tr) Edition {
	e := Edition{ID: fixtureID(), MBID: mbid, Country: country, ReleaseDate: date, Status: "Official", Source: "musicbrainz", TracksFetched: true}
	for d, tracks := range discs {
		m := Medium{ID: fixtureID(), Position: d + 1, Format: format, TrackCount: len(tracks)}
		for i, t := range tracks {
			m.Tracks = append(m.Tracks, ReleaseTrack{
				ID: fixtureID(), RecordingID: fixtureID(), RecordingMBID: t.rec, Position: i + 1, Title: t.title, LengthMs: t.sec * 1000,
			})
		}
		e.Media = append(e.Media, m)
		e.TrackCount += len(tracks)
	}
	e.DiscCount = len(discs)
	e.MediaSummary = format
	if len(discs) > 1 {
		e.MediaSummary = fmt.Sprintf("%d×%s", len(discs), format)
	}
	return e
}
