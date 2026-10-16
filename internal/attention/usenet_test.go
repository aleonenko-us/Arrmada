package attention

import (
	"context"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
)

// A usenet job SABnzbd failed: a full disk is an error with the fix in plain words, Arrmada's
// own grab or not; a bad release Arrmada grabbed is left to the stall check, which replaces
// it on its own; a job nobody grabbed shows with SABnzbd's reason.
func TestUsenetFailuresInNeedsYou(t *testing.T) {
	u := download.TransportUsenet
	failed := func(key, name, msg string) download.Item {
		return download.Item{Hash: key, Name: name, State: "error", RawState: "Failed", Transport: u, FailMessage: msg}
	}
	frame := Frame{OK: true, Whole: true, Queue: []download.Item{
		failed("sab-disk", "Film.Disk", "Unpacking failed, write error or disk is full?"),
		failed("sab-bad", "Film.Bad", "Download failed - Not on your server(s) - https://sabnzbd.org/not-complete"),
		failed("sab-other", "Film.Other", "Duplicate NZB"),
		{Hash: "sab-unpacking", Name: "Film.Unpacking", State: "unpacking", RawState: "Extracting", Transport: u, Progress: 1},
	}, Acq: map[string]automation.Acquisition{
		"sab-disk": {Title: "Film.Disk", Status: "grabbed"},
		"sab-bad":  {Title: "Film.Bad", Status: "grabbed"},
	}}
	s := New(staticFrame(frame), nil, quiet, Downloads())
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	items := byKey(s.Current().Items)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want the full disk and the job nobody grabbed", items)
	}
	disk, ok := items[KindDownload+":sab-disk"]
	if !ok || disk.Level != LevelError || !disk.Grabbed ||
		!strings.Contains(disk.Title, "SABnzbd couldn't save Film.Disk: the disk is full") ||
		!strings.Contains(disk.Detail, "Free up space") || !strings.Contains(disk.Detail, "write error or disk is full?") {
		t.Errorf("full disk = %+v", disk)
	}
	if _, ok := items[KindDownload+":sab-bad"]; ok {
		t.Error("a bad release Arrmada is replacing on its own shouldn't need anyone")
	}
	if other := items[KindDownload+":sab-other"]; other.Title != "Film.Other failed in SABnzbd" || other.Detail != "Duplicate NZB" || other.Grabbed {
		t.Errorf("a job nobody grabbed = %+v", other)
	}
}

// A handed-over link SABnzbd couldn't fetch either (the indexer turned it away too) is the
// indexer's, not the release's: an error saying so, with what to check, never a blocklist.
func TestUsenetFetchFailureInNeedsYou(t *testing.T) {
	frame := Frame{OK: true, Whole: true, Queue: []download.Item{{
		Hash: "sab-fetch", Name: "Film.Fetch - https://indexer.example/getnzb/x.nzb", State: "error", RawState: "Failed",
		Transport: download.TransportUsenet, FailMessage: "URL Fetching failed; Unauthorized access",
	}}, Acq: map[string]automation.Acquisition{"sab-fetch": {Title: "Film.Fetch", Status: "grabbed"}}}
	s := New(staticFrame(frame), nil, quiet, Downloads())
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	it, ok := byKey(s.Current().Items)[KindDownload+":sab-fetch"]
	if !ok || it.Level != LevelError || !it.Grabbed ||
		it.Title != "SABnzbd couldn't fetch the NZB for Film.Fetch: the indexer didn't let SABnzbd download the NZB either" ||
		!strings.Contains(it.Detail, "Check that the indexer lets SABnzbd download") || !strings.Contains(it.Detail, "Unauthorized access") {
		t.Errorf("fetch failure = %+v", it)
	}
}
