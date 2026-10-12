package attention

import (
	"context"
	"testing"

	"github.com/tristenlammi/arrmada/internal/issues"
)

type openIssues []issues.Issue

func (o openIssues) Open(context.Context, int) ([]issues.Issue, error) { return o, nil }

// Each open reported problem is an item that opens its issue; the group counts them and
// links to Requests → Issues, and no alert is raised from the feed (issue.reported is).
func TestIssuesProvider(t *testing.T) {
	src := openIssues{
		{ID: 4, Title: "Severance", Season: 1, Episode: 4, Category: "audio", Status: issues.StatusOpen, ReportedByName: "sam", CreatedAt: 100},
		{ID: 5, Title: "Dune", Year: 2021, Category: "video", Status: issues.StatusFixing, CreatedAt: 200},
	}
	items, err := Issues(src).Collect(context.Background(), &Frame{})
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if it := items[0]; it.Key != "issue:4" || it.Title != "sam reported a problem with Severance S01E04" || it.Detail != "Sound" ||
		it.Link != "/requests?tab=issues&issue=4" || it.Since != 100_000 {
		t.Errorf("first item = %+v", it)
	}
	if it := items[1]; it.Title != "Someone reported a problem with Dune (2021)" || it.Detail != "Picture · fixing" {
		t.Errorf("second item = %+v", it)
	}
	c := countItems(items)
	if c.Issues != 2 || c.Total != 2 {
		t.Errorf("counts = %+v", c)
	}
	g := groupItems(items)
	if len(g) != 1 || g[0].Title != "2 reported problems are open" || g[0].Link != "/requests?tab=issues" {
		t.Errorf("groups = %+v", g)
	}
	for _, it := range items {
		if eventFor(it) != "" {
			t.Errorf("%s raises an alert from the feed", it.Key)
		}
	}
}
