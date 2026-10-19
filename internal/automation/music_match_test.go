package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
)

// editionsProvider is fakeMusicProvider that also lists editions (MUS-30), from canned
// releases.
type editionsProvider struct {
	*fakeMusicProvider
	releases map[string][]metadata.ReleaseSummary
	details  map[string]metadata.ReleaseDetail
}

func (p *editionsProvider) AlbumReleases(_ context.Context, rg string) ([]metadata.ReleaseSummary, error) {
	return p.releases[rg], nil
}

func (p *editionsProvider) ReleaseTracks(_ context.Context, rel string) (metadata.ReleaseDetail, error) {
	d, ok := p.details[rel]
	if !ok {
		return d, fmt.Errorf("no release %s", rel)
	}
	return d, nil
}

// okcTitles is the standard edition; the deluxe adds three.
var okcTitles = []string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien", "Exit Music", "Let Down", "Karma Police"}
var okcBonus = []string{"Polyethylene", "Pearly", "Meeting in the Aisle"}

func release(mbid, date, country string, titles []string) (metadata.ReleaseSummary, metadata.ReleaseDetail) {
	md := metadata.ReleaseMedium{Position: 1, Format: "CD", TrackCount: len(titles)}
	for i, t := range titles {
		md.Tracks = append(md.Tracks, metadata.ReleaseTrack{MBID: mbid + fmt.Sprintf("-t%d", i+1),
			RecordingMBID: "rec-" + strings.ToLower(strings.ReplaceAll(t, " ", "-")), Position: i + 1, Title: t,
			LengthMs: (200 + i*17) * 1000})
	}
	sum := metadata.ReleaseSummary{MBID: mbid, Title: "OK Computer", Status: "Official", Date: date, Country: country,
		Credit: []metadata.CreditPart{{ArtistMBID: "a1", ArtistName: "Radiohead", Name: "Radiohead"}},
		Media:  []metadata.ReleaseMedium{{Position: 1, Format: "CD", TrackCount: len(titles)}}}
	det := metadata.ReleaseDetail{ReleaseSummary: sum, ReleaseGroupMBID: "rg1"}
	det.Media = []metadata.ReleaseMedium{md}
	return sum, det
}

// matchRig is newImportRig with editions: the album OK Computer has a 6-track standard
// (1997) and a 9-track deluxe (2009), and a finished download in the music category.
type matchRig struct {
	*importRig
	tags map[string]music.FileTags // file name → tags (nil: untagged)
	lens map[string]int            // file name → length ms
}

func newMatchRig(t *testing.T, grabbed bool, files []string) *matchRig {
	t.Helper()
	h := newStallHarness(t)
	base := &fakeMusicProvider{
		artists: map[string]metadata.ArtistDetails{"a1": {ArtistResult: metadata.ArtistResult{MBID: "a1", Name: "Radiohead"}}},
		albums:  map[string][]metadata.AlbumResult{"a1": {{MBID: "rg1", Title: "OK Computer", Type: "Album", ReleaseDate: "1997-05-21", Year: 1997}}},
		tracks:  map[string][]metadata.TrackResult{},
	}
	std, stdD := release("rel-std", "1997-05-21", "GB", okcTitles)
	dlx, dlxD := release("rel-dlx", "2009-03-24", "XE", append(append([]string(nil), okcTitles...), okcBonus...))
	prov := &editionsProvider{fakeMusicProvider: base,
		releases: map[string][]metadata.ReleaseSummary{"rg1": {dlx, std}},
		details:  map[string]metadata.ReleaseDetail{"rel-std": stdD, "rel-dlx": dlxD}}
	svc := music.NewService(h.c.db, prov, h.c.log)
	h.c.music = svc
	h.c.imp = library.NewImporter(t.TempDir(), h.c.log)
	r := &matchRig{importRig: &importRig{h: h, svc: svc, release: "Radiohead - OK Computer (1997) [FLAC]", bin: t.TempDir()},
		tags: map[string]music.FileTags{}, lens: map[string]int{}}
	h.c.imp.SetRecycleDir(r.bin)
	if err := os.MkdirAll(h.c.imp.MusicDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := svc.AddArtist(h.ctx, "a1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	r.artist, r.album = a, a.Albums[0]
	if err := svc.EnsureTracks(h.ctx, r.album); err != nil {
		t.Fatal(err)
	}
	h.c.musicTagsFn = func(path string) (music.FileTags, int, error) {
		name := filepath.Base(path)
		return r.tags[name], r.lens[name], nil
	}
	r.dl = filepath.Join(t.TempDir(), r.release)
	for _, f := range files {
		blobTrack(t, r.dl, f, 1)
	}
	if grabbed {
		h.c.recordMusicGrab(h.ctx, "music", r.album.ID, r.release, "Fake", "", hashFor(r.release))
	}
	h.qbit.mu.Lock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hashFor(r.release), "name": r.release, "state": "uploading", "progress": 1.0,
		"size": 600 << 10, "amount_left": 0, "category": download.CategoryMusic, "content_path": r.dl,
	})
	h.qbit.mu.Unlock()
	return r
}

// tagAll gives each named file full tags for the given titles, in order, with lengths
// matching the canned listing.
func (r *matchRig) tagAll(files, titles []string) {
	for i, f := range files {
		r.tags[f] = music.FileTags{Title: titles[i], Artist: "Radiohead", AlbumArtist: "Radiohead", Album: "OK Computer", Track: i + 1}
		r.lens[f] = (200 + i*17) * 1000
	}
}

func trackFileNames(titles []string) []string {
	out := make([]string, len(titles))
	for i, t := range titles {
		out[i] = fmt.Sprintf("%02d - %s.flac", i+1, t)
	}
	return out
}

// MUS-30/31: the album is listed with its editions and its target is the original
// (standard) edition; a grabbed deluxe download completes it by switching to the deluxe.
func TestMatcherDeluxeDownloadSwitchesEdition(t *testing.T) {
	all := append(append([]string(nil), okcTitles...), okcBonus...)
	files := trackFileNames(all)
	r := newMatchRig(t, true, files)
	r.tagAll(files, all)
	before, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if before.EditionTracks != 6 {
		t.Fatalf("target before: %d tracks, want the 6-track standard (the original policy)", before.EditionTracks)
	}
	r.h.c.ImportMusicDownloads(r.h.ctx)
	after, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if after.EditionTracks != 9 || after.HaveTracks != 9 || !after.Complete() {
		t.Fatalf("after the import: %d/%d of a %d-track edition, want the deluxe complete", after.HaveTracks, after.TrackCount, after.EditionTracks)
	}
	if s := r.grabStatus(t); s != grabStatusImported {
		t.Errorf("grab %q, want imported", s)
	}
	if n := artistEvents(t, r.svc, r.artist.ID, "Switched"); n != 1 {
		t.Errorf("history lines about the switch = %d, want 1", n)
	}
	// Switching back to the standard by hand loses no file.
	eds, _ := r.svc.Editions(r.h.ctx, r.album.ID)
	var std int64
	for _, e := range eds {
		if e.MBID == "rel-std" {
			std = e.ID
		}
	}
	if _, err := r.svc.SetEdition(r.h.ctx, r.album.ID, std, true); err != nil {
		t.Fatal(err)
	}
	back, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if !back.Complete() || back.EditionTracks != 6 {
		t.Errorf("back on the standard: %d/%d", back.HaveTracks, back.TrackCount)
	}
	files2, _ := r.svc.GetAlbum(r.h.ctx, r.album.ID)
	if len(files2.Extras) != 3 {
		t.Errorf("the deluxe's bonus files: %d extras, want 3 kept", len(files2.Extras))
	}
}

// Untagged files with good names: imported when grabbed for the album, held for review
// (with the matcher's explanation) when nobody grabbed it.
func TestMatcherStrippedTagsNeedAGrab(t *testing.T) {
	files := trackFileNames(okcTitles)
	r := newMatchRig(t, true, files)
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID); al.HaveTracks != 6 {
		t.Fatalf("grabbed: have %d, want all 6", al.HaveTracks)
	}

	r2 := newMatchRig(t, false, files)
	r2.h.c.ImportMusicDownloads(r2.h.ctx)
	rv := reviewFor(t, r2.h.c, hashFor(r2.release))
	if rv.ReasonCode != ReasonLowConfidence || rv.ExpectedID != r2.album.ID || !strings.Contains(rv.Reason, "file names") {
		t.Fatalf("review %+v", rv)
	}
	var rep music.MatchReport
	if err := json.Unmarshal(rv.Match, &rep); err != nil || len(rep.Candidates) == 0 || rep.Candidates[0].Edition == "" {
		t.Fatalf("breakdown %s (%v)", rv.Match, err)
	}
	if rep.Fingerprint != "off" {
		t.Errorf("fingerprint = %q, want off (Reviews says how to turn AcoustID on)", rep.Fingerprint)
	}
	if al, _ := r2.svc.AlbumRecord(r2.h.ctx, r2.album.ID); al.HaveTracks != 0 {
		t.Errorf("a held download placed %d tracks", al.HaveTracks)
	}
	if _, err := os.Stat(filepath.Join(r2.dl, files[0])); err != nil {
		t.Errorf("the download must be untouched: %v", err)
	}
}

// One file that is something else entirely: the rest import, it stays in the download,
// and the grab closes keeping the download's files.
func TestMatcherLeavesTheOffTrack(t *testing.T) {
	files := trackFileNames(okcTitles)
	r := newMatchRig(t, true, files)
	r.tagAll(files, okcTitles)
	r.tags[files[3]] = music.FileTags{Title: "Totally Different Song", Album: "OK Computer"}
	r.lens[files[3]] += 90000
	r.h.c.ImportMusicDownloads(r.h.ctx)
	al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if al.HaveTracks != 5 {
		t.Fatalf("have %d, want the other 5", al.HaveTracks)
	}
	if s := r.grabStatus(t); s != grabStatusDismissed {
		t.Errorf("grab %q, want dismissed (a file was left for a person)", s)
	}
	if _, err := os.Stat(filepath.Join(r.dl, files[3])); err != nil {
		t.Errorf("the off file must still be in the download: %v", err)
	}
}

// A 2-file download of a 6-track album: held as a partial, unless those are exactly the
// album's missing tracks.
func TestMatcherPartialRules(t *testing.T) {
	files := trackFileNames(okcTitles)[4:]
	r := newMatchRig(t, true, files)
	r.tagAll(files, okcTitles[4:])
	for i, f := range files {
		tg := r.tags[f]
		tg.Track = i + 5
		r.tags[f] = tg
		r.lens[f] = (200 + (i+4)*17) * 1000
	}
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if rv := reviewFor(t, r.h.c, hashFor(r.release)); rv.ReasonCode != ReasonPartial {
		t.Fatalf("review %q (%s), want partial", rv.ReasonCode, rv.Reason)
	}

	// The same two files when the album already holds the other four: exactly what's wanted.
	r2 := newMatchRig(t, true, files)
	r2.tagAll(files, okcTitles[4:])
	for i, f := range files {
		tg := r2.tags[f]
		tg.Track = i + 5
		r2.tags[f] = tg
		r2.lens[f] = (200 + (i+4)*17) * 1000
	}
	tracks, _ := r2.svc.Tracks(r2.h.ctx, r2.album.ID)
	for i, tk := range tracks[:4] {
		p := blobTrack(t, filepath.Join(r2.h.c.imp.MusicDir(), "Radiohead", "OK Computer (1997)"), fmt.Sprintf("%02d.flac", i+1), 7)
		if err := r2.svc.MarkTrackImported(r2.h.ctx, tk.ID, p, string(music.QualityFLAC), 900, 1, "old"); err != nil {
			t.Fatal(err)
		}
	}
	r2.h.c.ImportMusicDownloads(r2.h.ctx)
	if r2.h.c.hasReview(r2.h.ctx, hashFor(r2.release)) {
		t.Fatal("exactly the missing tracks went to review")
	}
	if al, _ := r2.svc.AlbumRecord(r2.h.ctx, r2.album.ID); !al.Complete() {
		t.Errorf("have %d/%d, want complete", al.HaveTracks, al.TrackCount)
	}
}

// fakeFingerprinter names recordings by file name.
type fakeFingerprinter struct {
	recs  map[string][]string
	calls int
}

func (f *fakeFingerprinter) Enabled() bool { return true }
func (f *fakeFingerprinter) Recordings(_ context.Context, path string) ([]string, error) {
	f.calls++
	return f.recs[filepath.Base(path)], nil
}

// MUS-32: untagged files named track01.flac… are identified by sound and imported when
// grabbed for the album.
func TestMatcherFingerprintSecondPass(t *testing.T) {
	files := []string{"track01.flac", "track02.flac", "track03.flac", "track04.flac", "track05.flac", "track06.flac"}
	r := newMatchRig(t, false, files)
	fp := &fakeFingerprinter{recs: map[string][]string{}}
	// The files are in a scrambled order: track01 is Karma Police, and so on.
	order := []int{5, 0, 3, 1, 4, 2}
	for i, f := range files {
		title := okcTitles[order[i]]
		fp.recs[f] = []string{"rec-" + strings.ToLower(strings.ReplaceAll(title, " ", "-"))}
		r.lens[f] = (200 + order[i]*17) * 1000
	}
	r.h.c.SetMusicFingerprinter(fp)
	// Grabbed for the album, so the identification is enough to import.
	r.h.c.recordMusicGrab(r.h.ctx, "music", r.album.ID, r.release, "Fake", "", hashFor(r.release))
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if fp.calls == 0 {
		t.Fatal("the second pass never ran")
	}
	tracks, _ := r.svc.Tracks(r.h.ctx, r.album.ID)
	for i, f := range files {
		tk := tracks[order[i]]
		if !tk.HasFile {
			t.Errorf("%s (%s) wasn't placed", tk.Title, f)
		}
	}
	if r.h.c.hasReview(r.h.ctx, hashFor(r.release)) {
		rv := reviewFor(t, r.h.c, hashFor(r.release))
		t.Fatalf("held: %s %s", rv.ReasonCode, rv.Reason)
	}
}

func TestNeedsFingerprint(t *testing.T) {
	cand := &music.MatchCandidate{}
	cases := []struct {
		name  string
		best  *music.Scored
		files []music.MatchFile
		want  bool
	}{
		{"no candidate at all", nil, nil, true},
		{"a close, complete, tagged match", &music.Scored{Cand: cand, Distance: 0.05, Pairs: []music.Pair{{Distance: 0.1}}},
			[]music.MatchFile{{Tags: music.FileTags{Title: "x", RecordingMBID: "r"}}}, false},
		{"over 0.15", &music.Scored{Cand: cand, Distance: 0.2}, []music.MatchFile{{Tags: music.FileTags{Title: "x"}}}, true},
		{"tracks missing", &music.Scored{Cand: cand, Distance: 0.05, Missing: []int{3}}, []music.MatchFile{{Tags: music.FileTags{Title: "x"}}}, true},
		{"an extra file", &music.Scored{Cand: cand, Distance: 0.05, Extra: []int{1}}, []music.MatchFile{{Tags: music.FileTags{Title: "x"}}}, true},
		{"one track far off", &music.Scored{Cand: cand, Distance: 0.05, Pairs: []music.Pair{{Distance: 0.5}}}, []music.MatchFile{{Tags: music.FileTags{Title: "x"}}}, true},
		{"no MBIDs, few titles", &music.Scored{Cand: cand, Distance: 0.01},
			[]music.MatchFile{{Tags: music.FileTags{Title: "x"}}, {}, {}}, true},
		{"no MBIDs, mostly titled", &music.Scored{Cand: cand, Distance: 0.01},
			[]music.MatchFile{{Tags: music.FileTags{Title: "x"}}, {Tags: music.FileTags{Title: "y"}}, {}}, false},
	}
	for _, c := range cases {
		res := music.MatchResult{Best: c.best}
		if got := needsFingerprint(res, c.files); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// Reviews' "import into this edition": the files go on the chosen edition and the album
// switches to it.
func TestReviewImportIntoChosenEdition(t *testing.T) {
	all := append(append([]string(nil), okcTitles...), okcBonus...)
	files := trackFileNames(all)
	r := newMatchRig(t, false, files)
	eds, _ := r.svc.Editions(r.h.ctx, r.album.ID)
	var dlx int64
	for _, e := range eds {
		if e.MBID == "rel-dlx" {
			dlx = e.ID
		}
	}
	res, err := r.h.c.db.Exec(`INSERT INTO import_reviews (hash, name, content_path, media_type, expected_id, reason, reason_code)
		VALUES (?, ?, ?, 'music', ?, 'held', 'low_confidence')`, hashFor(r.release), r.release, r.dl, r.album.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := r.h.c.ImportReview(WithMusicEdition(r.h.ctx, dlx), id, 0, ""); err != nil {
		t.Fatal(err)
	}
	al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if al.TargetEditionID != dlx || al.HaveTracks != 9 {
		t.Errorf("target %d (want %d), have %d (want 9)", al.TargetEditionID, dlx, al.HaveTracks)
	}
}
