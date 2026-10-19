package health

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// One finding per affected show, linking to the show; past ten, one more counts the rest.
func TestMisplacedEpisodesCheck(t *testing.T) {
	var shows []MisplacedShow
	for i := 1; i <= 12; i++ {
		shows = append(shows, MisplacedShow{SeriesID: int64(i), Title: fmt.Sprintf("Show %d", i), Files: 2, NoFolder: i == 1})
	}
	got := MisplacedEpisodesCheck(func(context.Context) ([]MisplacedShow, error) { return shows, nil }).Run(context.Background())
	if len(got) != maxMisplacedFindings+1 {
		t.Fatalf("%d findings, want %d", len(got), maxMisplacedFindings+1)
	}
	if got[0].Fix == nil || got[0].Fix.Path != "/series/1?fix=misplaced" || !strings.Contains(got[0].Message, "Show 1: 2 episode files") ||
		!strings.Contains(got[0].Message, "won't import") {
		t.Errorf("first finding = %+v", got[0])
	}
	if last := got[len(got)-1]; !strings.Contains(last.Message, "2 more shows have 4 episode files") {
		t.Errorf("last finding = %+v", last)
	}
	none := MisplacedEpisodesCheck(func(context.Context) ([]MisplacedShow, error) { return nil, errors.New("db") }).Run(context.Background())
	if len(none) != 0 {
		t.Errorf("an unreadable list reported %v", none)
	}
}
