package attention

import (
	"context"
	"testing"

	"github.com/tristenlammi/arrmada/internal/subtitles"
)

type fakeSubs []subtitles.Actionable

func (f fakeSubs) Actionable(context.Context) ([]subtitles.Actionable, error) { return f, nil }

// Each parked reason is one counted item; a rejected login is an error.
func TestSubtitlesProvider(t *testing.T) {
	items, err := Subtitles(fakeSubs{
		{Reason: string(subtitles.ResultAuthFailed), Count: 12, Since: 100, Detail: "OpenSubtitles login rejected (401)"},
		{Reason: string(subtitles.ResultModelMissing), Count: 3},
	}).Collect(context.Background(), &Frame{})
	if err != nil || len(items) != 2 {
		t.Fatalf("items %+v err %v", items, err)
	}
	if it := items[0]; it.Key != "subtitles:auth_failed" || it.Level != LevelError || it.Count != 12 || it.Since != 100000 || it.Link != "/subtitles" {
		t.Errorf("auth item %+v", it)
	}
	c := countItems(items)
	if c.Subtitles != 15 || c.Total != 15 {
		t.Errorf("counts %+v", c)
	}
	if g := groupItems(items); len(g) != 1 || g[0].Title != "15 subtitles are waiting on you" {
		t.Errorf("groups %+v", g)
	}
}
