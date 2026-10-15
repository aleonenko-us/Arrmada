package automation

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// Ranking v2 is the default, and its upgrade sweeps and import gates share one verdict:
// whatever the sweep grabs as an upgrade, the import gate takes when it arrives — no
// grab-then-refuse — under v2 and under classic ranking alike.

// upgradeOffers are single-episode releases of S04E06 the series sweep might take over the
// file on disk (a 1080p WEB-DL H.264 from EVO): resolution, preference, custom, source and
// PROPER gains, and releases that only win on a key that never upgrades. (Every sweep is a
// real indexer search behind the per-host throttle, so the list is kept short; the quality
// package checks the sweep against the gate over a far wider fixture set.)
var upgradeOffers = []string{
	"Show.S04E06.2160p.WEB-DL.DDP5.1.H.265-NTb",
	"Show.S04E06.1080p.WEB-DL.DDP5.1.Atmos.H.264-GRPB",
	"Show.S04E06.1080p.BluRay.x264-GRPD",
	"Show.S04E06.1080p.WEB-DL.DDP5.1.H.264.PROPER-EVO",
	"Show.S04E06.1080p.WEB-DL.DDP5.1.H.264.PROPER-GRPE",
	"Show.S04E06.1080p.WEB-DL.IMAX.H.264-GRPG",
	"Show.S04E06.1080p.WEBRip.x264-GRPF",
	"Show.S04E06.720p.WEB-DL.DDP5.1.H.265-GRPH",
}

// targetShow is an upgrade show on a target profile (prefers HEVC, HDR10 and Atmos, a
// custom IMAX keyword) under trigger, ranking in mode ("" = the default), with S04E06 on
// disk as EVO's 1080p WEB-DL H.264.
func targetShow(t *testing.T, mode, trigger string) upgradeShow {
	t.Helper()
	u := newUpgradeShow(t, false)
	if mode != "" {
		useRanking(t, u.h, mode)
	}
	sp, err := u.h.c.quality.Create(u.h.ctx, quality.StoredProfile{MediaType: quality.MediaSeries, Name: "Target TV",
		UpgradesEnabled: true, UpgradeTrigger: trigger, Keywords: []quality.Keyword{{Term: "IMAX", Score: 30}},
		Ideal: &quality.IdealFile{Codec: map[string]string{"hevc": quality.PrefWant}, HDR: map[string]string{"HDR10": quality.PrefWant},
			Audio: map[string]string{"atmos": quality.PrefWant}}})
	if err != nil {
		t.Fatal(err)
	}
	u.exec(t, `UPDATE series SET quality_profile = ? WHERE id = ?`, fmt.Sprintf("custom:%d", sp.ID), u.id)
	path := filepath.Join(t.TempDir(), "Show", "Season 04", "Show - S04E06.mkv")
	const size = int64(2 << 30)
	sparseFile(t, path, size)
	u.addEpisode(t, 4, 6, 36, path, "Show.S04E06.1080p.WEB-DL.DDP5.1.H.264-EVO.mkv", size, false)
	return u
}

// Every release the series sweep grabs as an upgrade is taken by the series import gate,
// one offer at a time, under v2 (the default) with a broad and a narrow trigger, and under
// classic ranking.
func TestSeriesSweepUpgradesPassTheImportGate(t *testing.T) {
	for _, run := range []struct{ mode, trigger string }{
		{"", quality.TriggerAny}, {"", quality.TriggerFormat}, {quality.RankingV1, quality.TriggerAny},
	} {
		t.Run(fmt.Sprintf("mode=%q/%s", run.mode, run.trigger), func(t *testing.T) {
			grabbed := 0
			for _, offer := range upgradeOffers {
				u := targetShow(t, run.mode, run.trigger)
				u.h.ix.offer(offer)
				if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
					t.Fatal(err)
				}
				if len(u.grabbedTitles(t)) == 0 {
					continue
				}
				grabbed++
				if !importGateTakes(t, u, offer+".mkv") {
					t.Errorf("%s: the sweep grabbed it as an upgrade and the import gate refuses it", offer)
				}
			}
			if grabbed == 0 {
				t.Fatal("the sweep took none of the offers — nothing was checked")
			}
		})
	}
}

// Under v2 the sweep doesn't take a release that only wins on the release group, and the
// import gate agrees; a better-known group's copy of the file is no reason to re-download.
func TestV2SeriesSweepLeavesAGroupOnlyGain(t *testing.T) {
	u := targetShow(t, "", quality.TriggerAny)
	const flux = "Show.S04E06.1080p.WEB-DL.DDP5.1.H.264-FLUX" // FLUX is a default tier-1 group
	u.h.ix.offer(flux)
	if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
		t.Fatal(err)
	}
	if got := u.grabbedTitles(t); len(got) != 0 {
		t.Fatalf("grabbed %v on the release group alone", got)
	}
	if importGateTakes(t, u, flux+".mkv") {
		t.Error("the import gate would replace the file on the release group alone")
	}
}

// The movie sweep under v2 decides with the same verdict: each track's pick is an upgrade
// the quality service's import-side checks agree on, and never a lower resolution (the one
// thing the movie importer refuses on its own).
func TestV2MovieSweepPicksAreTheSameVerdict(t *testing.T) {
	offers := []string{
		"Heat.1995.2160p.WEB-DL.DDP5.1.H.265-NTb",
		"Heat.1995.1080p.BluRay.x264-GRPD",
		"Heat.1995.1080p.WEB-DL.H.265-GRPA",
		"Heat.1995.1080p.WEB-DL.H.264-FLUX",
		"Heat.1995.720p.WEB-DL.H.264.PROPER-OLD",
		"Heat.1995.720p.BluRay.x264-GRPB",
		"Heat.1995.480p.DVDRip.x264-GRPC",
	}
	took := 0
	for _, offer := range offers {
		h, id, ref := upgradeMovieHarness(t)
		h.ix.offer(offer)
		sweepMovie(t, h, id)
		rows := movieGrabRows(t, h, id)
		if len(rows) == 0 {
			continue
		}
		took++
		m, err := h.c.movies.Get(h.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		cur := h.c.CurrentMovieFile(h.ctx, m)
		candRank := parser.ResolutionRank(parser.Parse(offer).Resolution)
		curRank := parser.ResolutionRank(parser.Parse(cur.Release).Resolution)
		if candRank < curRank {
			t.Errorf("%s: a lower resolution than %s was grabbed — the importer refuses it", offer, cur.Release)
		}
		if candRank == curRank && !h.c.quality.IsQualityUpgrade(h.ctx, ref, offer, 4, cur) {
			t.Errorf("%s: grabbed as an upgrade of %s, but the quality verdict refuses it", offer, cur.Release)
		}
	}
	if took == 0 {
		t.Fatal("the sweep took none of the offers")
	}
}
