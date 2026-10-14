package quality

import (
	"strconv"
	"testing"
)

// The file's recorded name is its downloaded file's; the indexer may title the same torrent
// differently. They are one release when the group and every tag agree.
func TestSameReleaseAcrossNamings(t *testing.T) {
	const file = "Reacher.S04E06.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-NTb.mkv"
	cases := []struct {
		name      string
		other     string
		withCodec bool
		want      bool
	}{
		{"indexer rewrite", "Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb", true, true},
		{"episode title in one name", "Reacher S04E06 Bad Blood 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb", true, true},
		{"year only one states", "Reacher 2022 S04E06 1080p AMZN WEB-DL DDP5 1 Atmos H 264-NTb", true, true},
		{"another group", "Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-FLUX", true, false},
		{"a proper", "Reacher S04E06 PROPER 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb", true, false},
		{"another episode", "Reacher S04E07 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb", true, false},
		{"another service", "Reacher S04E06 1080p NF WEB-DL DD+ 5 1 Atmos H 264-NTb", true, false},
		{"no Atmos", "Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 H 264-NTb", true, false},
		{"another resolution", "Reacher S04E06 2160p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb", true, false},
		{"another codec", "Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos H 265-NTb", true, false},
		{"another codec, codec aside", "Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos H 265-NTb", false, true},
	}
	for _, c := range cases {
		if got := sameRelease(file, c.other, c.withCodec); got != c.want {
			t.Errorf("%s: sameRelease(%q) = %v, want %v", c.name, c.other, got, c.want)
		}
	}
	// Without a group two names with the same tags can be two encodes.
	if sameRelease("Show.S01E01.1080p.WEB-DL.x264.mkv", "Show S01E01 1080p WEB-DL x264", true) {
		t.Error("two group-less names must not count as one release")
	}
}

// The upgrade sweep and the import gate agree that the file's own release, under the
// indexer's title, is no upgrade — even when the file's probe misses a track its name has
// and the name therefore outscores the file.
func TestOwnReleaseUnderAnotherTitleIsNoUpgrade(t *testing.T) {
	s, ctx := testService(t)
	sp, err := s.Create(ctx, StoredProfile{MediaType: MediaSeries, Name: "Atmos TV", UpgradesEnabled: true,
		Ideal: &IdealFile{Audio: map[string]string{"atmos": PrefWant}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(sp.ID, 10)
	cur := withFacts("Reacher.S04E06.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-NTb.mkv", 4, 50, FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR"})
	own := NewCandidate("Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb", 4, 40).WithRuntime(50)
	if pick, ok := s.UpgradeCandidate(ctx, ref, cur, []Candidate{own}); ok {
		t.Errorf("picked %q, the file's own release, as its upgrade", pick.Name)
	}
	if s.IsQualityUpgrade(ctx, ref, own.Name, 4, cur) {
		t.Error("the import gate took the file's own release as its upgrade")
	}
	// Another group's release with the Atmos the probe didn't find is still one.
	flux := NewCandidate("Reacher S04E06 1080p AMZN WEB-DL DDP5 1 Atmos H 264-FLUX", 4, 40).WithRuntime(50)
	if pick, ok := s.UpgradeCandidate(ctx, ref, cur, []Candidate{own, flux}); !ok || pick.Name != flux.Name {
		t.Errorf("another group's Atmos release should be the upgrade, got %q %v", pick.Name, ok)
	}
}

// A converted file's restamped name and the indexer's title of the release it came from:
// the original is never re-grabbed over its conversion, however the indexer writes it.
func TestConvertedFromReleaseUnderAnotherTitle(t *testing.T) {
	s, ctx := testService(t)
	sp, err := s.Create(ctx, StoredProfile{MediaType: MediaSeries, Name: "Upgrades", UpgradesEnabled: true, UpgradeMinPercent: 20})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(sp.ID, 10)
	// No pre-conversion record: only the stamped name says what it was.
	cur := cf("Reacher.S04E06.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.265-NTb.mkv", 1.2, 50)
	orig := NewCandidate("Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb", 4, 40).WithRuntime(50)
	if pick, ok := s.UpgradeCandidate(ctx, ref, cur, []Candidate{orig}); ok {
		t.Errorf("re-grabbed %q, the release the file was converted from", pick.Name)
	}
	// With the record, the recorded original under the indexer's title is excluded too.
	cur.OrigRelease, cur.OrigSizeGB = "Reacher.S04E06.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-NTb.mkv", 1.5
	if pick, ok := s.UpgradeCandidate(ctx, ref, cur, []Candidate{orig}); ok {
		t.Errorf("re-grabbed %q, the recorded original", pick.Name)
	}
}
