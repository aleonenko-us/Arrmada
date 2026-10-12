package attention

import (
	"context"
	"fmt"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/issues"
)

// OpenIssues is the reported-problems source (*issues.Service).
type OpenIssues interface {
	Open(ctx context.Context, limit int) ([]issues.Issue, error)
}

type issuesProvider struct{ src OpenIssues }

// Issues reports each problem a requester reported that is still open or being fixed
// (issue:<id>). It raises no alert of its own: issue.reported announces each new one when
// it is reported, as request.created does for requests.
func Issues(src OpenIssues) Provider { return issuesProvider{src} }

func (issuesProvider) Name() string { return "issues" }

func (p issuesProvider) Collect(ctx context.Context, _ *Frame) ([]Item, error) {
	if p.src == nil {
		return nil, nil
	}
	list, err := p.src.Open(ctx, MaxItems)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(list))
	for _, i := range list {
		name := titleYear(i.Title, i.Year)
		if i.Episode > 0 {
			name = fmt.Sprintf("%s S%02dE%02d", i.Title, i.Season, i.Episode)
		}
		who := i.ReportedByName
		if who == "" {
			who = "Someone"
		}
		detail := issueWords(i.Category)
		if i.Status == issues.StatusFixing {
			detail += " · fixing"
		}
		out = append(out, Item{
			Key: KindIssue + ":" + strconv.FormatInt(i.ID, 10), Kind: KindIssue, Level: LevelWarning,
			Name:   name,
			Title:  who + " reported a problem with " + name,
			Detail: detail,
			Since:  i.CreatedAt * 1000,
			// Straight to the issue: the Issues tab opens it.
			Link: health.FixIssues.Path + "&issue=" + strconv.FormatInt(i.ID, 10), LinkLabel: health.FixIssues.Label,
		})
	}
	return out, nil
}

// issueWords is an issue category as the report sheet names it.
func issueWords(category string) string {
	switch category {
	case "video":
		return "Picture"
	case "audio":
		return "Sound"
	case "subtitles":
		return "Subtitles"
	case "wrong_file":
		return "Wrong file"
	}
	return "Something else"
}
