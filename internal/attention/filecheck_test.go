package attention

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/filecheck"
)

type fakeFileProblems []filecheck.Problem

func (f fakeFileProblems) Findings(context.Context, bool) ([]filecheck.Problem, error) { return f, nil }

func TestFileChecksItems(t *testing.T) {
	seen := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	cam := filecheck.Stored{ID: 4, Kind: filecheck.KindMovie, Code: filecheck.CodeSuspectedCam, Title: "Resident Evil (2026)",
		Detail: "probably a cam — grabbed 3 weeks before it was out at home", Release: "Resident.Evil.2026.1080p.WEB-DL-GRP", FirstSeen: seen}
	mute := filecheck.Stored{ID: 5, Kind: filecheck.KindMovie, Code: filecheck.CodeNoAudio, Title: "The Matrix (1999)", Detail: "the file has no sound", FirstSeen: seen}
	src := fakeFileProblems{
		{Stored: cam, State: filecheck.StateReplacing, Text: filecheck.ProblemText(cam, filecheck.StateReplacing)},
		{Stored: mute, Text: filecheck.ProblemText(mute, filecheck.StateFlagged)},
		{Stored: cam, State: filecheck.StateGaveUp, Error: true, Text: filecheck.ProblemText(cam, filecheck.StateGaveUp)},
	}
	items, err := FileChecks(src).Collect(context.Background(), &Frame{})
	if err != nil || len(items) != 3 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if it := items[0]; it.Key != "filecheck:4" || it.ID != 4 || it.Level != LevelWarning ||
		it.Title != "Resident Evil (2026): probably a cam — blocklisted it and looking for a better copy" ||
		it.Detail != "Probably a cam — grabbed 3 weeks before it was out at home" || it.Link != "/activity/needs-you#file-checks" ||
		it.Since != seen.UnixMilli() {
		t.Errorf("replacing item = %+v", it)
	}
	if it := items[1]; it.Title != "The Matrix (1999): the file has no sound" || it.Detail != "" {
		t.Errorf("flagged item = %+v", it)
	}
	if it := items[2]; it.Level != LevelError || it.Title != "Resident Evil (2026): probably a cam — automatic replacement gave up after 3 tries" {
		t.Errorf("gave-up item = %+v", it)
	}
	if got := groupTitle(KindFileCheck, 2); got != "2 files look wrong inside" {
		t.Errorf("group = %q", got)
	}
}
