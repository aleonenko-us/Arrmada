package arrimport

import "testing"

// GH-53: a show Sonarr had unmonitored comes over with its searching off — what gets
// monitored doesn't change — and the result counts it, so the summary can say so.
func TestUnmonitoredShowIsCounted(t *testing.T) {
	w := newWorld(t)
	c, snap := sonarrImport(t, w)
	for i := range snap.Series {
		if snap.Series[i].TVDBID == 81189 { // Breaking Bad
			snap.Series[i].Monitored = false
		}
	}
	res, err := w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bb := showByTMDB(t, w, 1396); bb.Monitored {
		t.Error("a show Sonarr had unmonitored should come over with its searching off")
	}
	if fr := showByTMDB(t, w, 209867); !fr.Monitored {
		t.Error("a show Sonarr had monitored should come over with its searching on")
	}
	if res.Imported != 2 || res.Unmonitored != 1 || res.Paced != 1 {
		t.Errorf("result = imported %d, unmonitored %d, paced %d; want 2, 1, 1", res.Imported, res.Unmonitored, res.Paced)
	}
}
