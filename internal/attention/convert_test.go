package attention

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/convert"
	"github.com/tristenlammi/arrmada/internal/notify"
)

type fakeStalls []convert.Stall

func (f fakeStalls) Stalls(context.Context) ([]convert.Stall, error) { return f, nil }

// Convert stuck for space is one Needs-you item per reason, linked to Convert's Problems,
// counted, and alerted as convert.stalled.
func TestConvertStallItems(t *testing.T) {
	since := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	src := fakeStalls{
		{Reason: convert.SkipNoScratch, Files: 3, Title: "Dune (2021)", Note: "needs ~91 GB of scratch in /transcode, it has 40 GB free", Since: since},
		{Reason: convert.SkipLibraryFull, Files: 1, Title: "Heat (1995)", Note: "needs ~30 GB free on the library disk"},
	}
	items, err := Convert(src).Collect(context.Background(), &Frame{})
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	got := byKey(items)
	it, ok := got["convert:stalled:no_scratch"]
	if !ok {
		t.Fatalf("keys = %v", got)
	}
	if it.Kind != KindConvert || it.Title != "Convert is stuck: the scratch folder is full" ||
		it.Detail != "Dune (2021) and 2 more: needs ~91 GB of scratch in /transcode, it has 40 GB free" ||
		it.Since != since.UnixMilli() || it.Link != "/convert?tab=problems" || it.LinkKey != "convertProblems" {
		t.Errorf("item = %+v", it)
	}
	if got["convert:stalled:library_full"].Detail != "Heat (1995): needs ~30 GB free on the library disk" {
		t.Errorf("single-file detail = %q", got["convert:stalled:library_full"].Detail)
	}
	if ev := eventFor(it); ev != notify.EventConvertStalled {
		t.Errorf("eventFor = %q", ev)
	}
	c := countItems(items)
	if c.Convert != 2 || c.Total != 2 {
		t.Errorf("counts = %+v", c)
	}
	if g := groupItems(items); len(g) != 1 || g[0].Title != "Convert is stuck waiting for disk space" || g[0].Link != "/convert?tab=problems" {
		t.Errorf("groups = %+v", g)
	}
	if items, _ := Convert(fakeStalls{}).Collect(context.Background(), &Frame{}); len(items) != 0 {
		t.Errorf("nothing stuck still made items: %+v", items)
	}
}
