package health

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A client whose finished downloads Arrmada can't see is a warning naming it, linked to
// its card; nothing unseen, or no queue to judge, is no finding.
func TestDownloadPathsCheck(t *testing.T) {
	run := func(list []UnseenDownloads, err error) []Finding {
		return DownloadPathsCheck(func(context.Context) ([]UnseenDownloads, error) { return list, err }).Run(context.Background())
	}
	if f := run(nil, nil); len(f) != 0 {
		t.Fatalf("nothing unseen = %+v", f)
	}
	if f := run(nil, errors.New("all download clients failed")); len(f) != 0 {
		t.Fatalf("no queue = %+v", f)
	}
	f := run([]UnseenDownloads{{ClientID: 3, ClientName: "Seedbox", Count: 4, Example: "/downloads/Film"}}, nil)
	if len(f) != 1 || f[0].Level != LevelWarning || f[0].Key != "downloads.paths.3" {
		t.Fatalf("finding = %+v", f)
	}
	if !strings.Contains(f[0].Message, "Seedbox has 4 finished downloads") || !strings.Contains(f[0].Message, "/downloads/Film") {
		t.Fatalf("message = %q", f[0].Message)
	}
	if f[0].Fix == nil || !strings.HasSuffix(f[0].Fix.Path, "#download_client-3") {
		t.Fatalf("fix = %+v", f[0].Fix)
	}
}
