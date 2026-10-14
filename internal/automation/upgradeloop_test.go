package automation

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// The Reacher loop: an older build grabbed "Reacher S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos
// H 264-NTb" as an upgrade every six hours, ten grab rows for one episode. These pin the two
// halves of it: the sweep must know the release on offer is the file it already has, and a
// release it has grabbed before is never grabbed again.

// atmosShow is an upgrade show whose profile prefers Atmos, with S04E06 on disk: the NTb
// release, recorded (as every import records it) by its downloaded file's name. The probe
// of the file didn't report the Atmos track the name claims — the disagreement that made
// the release look better than itself.
func atmosShow(t *testing.T) (upgradeShow, string) {
	t.Helper()
	u := newUpgradeShow(t, false)
	sp, err := u.h.c.quality.Create(u.h.ctx, quality.StoredProfile{MediaType: quality.MediaSeries, Name: "Atmos TV",
		UpgradesEnabled: true, Ideal: &quality.IdealFile{Audio: map[string]string{"atmos": quality.PrefWant}}})
	if err != nil {
		t.Fatal(err)
	}
	u.exec(t, `UPDATE series SET quality_profile = ? WHERE id = ?`, fmt.Sprintf("custom:%d", sp.ID), u.id)
	path := filepath.Join(t.TempDir(), "Show", "Season 04", "Show - S04E06.mkv")
	const size = int64(4 << 30) // the fake indexer offers every release at 4 GiB
	sparseFile(t, path, size)
	u.addEpisode(t, 4, 6, 36, path, "Show.S04E06.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-NTb.mkv", size, false)
	u.h.c.SetFileFacts(fakeFacts{path: {size: size, facts: quality.FileFacts{Resolution: "1080p", Codec: "h264", HDR: "SDR"}}})
	return u, path
}

// importGateTakes is the import gate's verdict on a downloaded file of S04E06 named file.
func importGateTakes(t *testing.T, u upgradeShow, file string) bool {
	t.Helper()
	s, err := u.h.c.series.Get(u.h.ctx, u.id)
	if err != nil {
		t.Fatal(err)
	}
	return u.h.c.wantsEpisodeFile(u.h.ctx, s, 4, 6, parser.Parse(file), file, 4<<30, 1)
}

// The indexer's title for the file's own release ("DD+ 5 1", spaces, no extension) is the
// same release as the recorded "….DDP5.1….mkv". The import gate, comparing the file's own
// name, always refused it; the sweep, comparing strings, took it for another release and
// grabbed it on the Atmos its name claims. It must not be grabbed at all.
func TestUpgradeSweepKnowsTheFileByItsIndexerTitle(t *testing.T) {
	u, _ := atmosShow(t)
	if importGateTakes(t, u, "Show.S04E06.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-NTb.mkv") {
		t.Fatal("precondition: the import gate keeps the file when its own release arrives")
	}
	u.h.ix.offer("Show S04E06 1080p AMZN WEB-DL DD+ 5 1 Atmos H 264-NTb")
	if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
		t.Fatal(err)
	}
	if got := u.grabbedTitles(t); len(got) != 0 {
		t.Fatalf("grabbed %v as an upgrade of the file it already is — the import gate refuses it on arrival", got)
	}
}

// Another group's release with the Atmos the probed file lacks is a real upgrade: the sweep
// grabs it and the import gate takes it, the same verdict on both sides.
func TestUpgradeSweepStillTakesAnotherGroupsBetterRelease(t *testing.T) {
	u, _ := atmosShow(t)
	const flux = "Show S04E06 1080p AMZN WEB-DL DDP5 1 Atmos H 264-FLUX"
	u.h.ix.offer(flux)
	if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
		t.Fatal(err)
	}
	if got := u.grabbedTitles(t); !reflect.DeepEqual(got, []string{flux}) {
		t.Fatalf("grabbed %v, want %s", got, flux)
	}
	if !importGateTakes(t, u, "Show.S04E06.1080p.AMZN.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv") {
		t.Error("the import gate must take the upgrade the sweep grabbed")
	}
}

// An upgrade is tried once. However its grab ended — imported, refused by the import gate,
// closed as already on disk — a sweep that finds the episode unchanged must not grab the
// same release again: that is a new grab row every six hours for a torrent the client
// already has, the rows the failover check later found piled up.
func TestUpgradeSweepTriesAReleaseOnce(t *testing.T) {
	u := newUpgradeShow(t, false)
	u.addEpisode(t, 1, 1, 1, "/lib/e1.mkv", "Show.S01E01.720p.WEB-DL.x264-OLD", 1<<30, false)
	const better = "Show.S01E01.1080p.WEB-DL.x264-NEW"
	u.h.ix.offer(better)
	if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
		t.Fatal(err)
	}
	if got := u.grabbedTitles(t); !reflect.DeepEqual(got, []string{better}) {
		t.Fatalf("first sweep grabbed %v, want %s", got, better)
	}
	// The grab is closed out, the episode still holds its old file.
	for _, status := range []string{grabStatusImported, grabStatusFailed} {
		u.exec(t, `UPDATE grabs SET status = ?`, status)
		if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
			t.Fatal(err)
		}
		if got := u.grabbedTitles(t); len(got) != 1 {
			t.Fatalf("after a %s grab the sweep grabbed %v — the same upgrade again", status, got)
		}
	}
}
