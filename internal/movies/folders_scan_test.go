package movies

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two Movies folders can each hold a folder of the same name: both are listed for review,
// each under its own folder, and settling one leaves the other.
func TestNeedsReviewKeepsSameNameInEachFolder(t *testing.T) {
	svc, _, movies, kids := foldersFixture(t)
	ctx := context.Background()
	videoFixture(t, filepath.Join(movies, "Mystery Film (2001)", "Mystery.Film.2001.mkv"))
	videoFixture(t, filepath.Join(kids.Path, "Mystery Film (2001)", "Mystery.Film.2001.mkv"))
	if _, err := svc.ScanLibrary(ctx, "", ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	un, err := svc.LastUnmatched(ctx)
	if err != nil || len(un) != 2 || un[0].Root == un[1].Root {
		t.Fatalf("unmatched = %+v, %v; want one row per folder", un, err)
	}
	// Settle the Kids one (as Other, tmdb 20): only that row goes.
	if err := svc.ImportFolderAs(ctx, kids.Path, "Mystery Film (2001)", 20); err != nil {
		t.Fatal(err)
	}
	un, _ = svc.LastUnmatched(ctx)
	if len(un) != 1 || un[0].Root != movies {
		t.Errorf("after the pick = %+v, want only the main folder's row", un)
	}
}

// One unreadable folder (a share that's down) is skipped and named, and the rest are
// scanned; its review rows stay. With only one folder, an unreadable one fails as before.
func TestScanSkipsAnUnreadableFolder(t *testing.T) {
	svc, _, movies, kids := foldersFixture(t)
	ctx := context.Background()
	videoFixture(t, filepath.Join(movies, "Other (2020)", "Other.2020.1080p.mkv"))
	videoFixture(t, filepath.Join(kids.Path, "Mystery Film (2001)", "Mystery.Film.2001.mkv"))
	if _, err := svc.ScanLibrary(ctx, "", ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(kids.Path); err != nil { // the Kids share goes away
		t.Fatal(err)
	}
	res, err := svc.ScanLibrary(ctx, "", ScanOptions{})
	if err != nil {
		t.Fatalf("scan with one folder down = %v, want the rest scanned", err)
	}
	if len(res.FailedFolders) != 1 || res.FailedFolders[0].Root != kids.Path || res.Skipped != 1 {
		t.Errorf("result = %+v", res)
	}
	if un, _ := svc.LastUnmatched(ctx); len(un) != 1 || un[0].Root != kids.Path {
		t.Errorf("the unreadable folder's review row = %+v, want it kept", un)
	}
	if err := os.RemoveAll(movies); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ScanLibrary(ctx, "", ScanOptions{}); err == nil {
		t.Error("a scan with every folder unreadable didn't fail")
	}

	single, _, root := scanFixture(t)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := single.ScanLibrary(ctx, root, ScanOptions{}); err == nil {
		t.Error("a one-folder scan of an unreadable folder didn't fail")
	}
}

// A scan of one folder (Settings → Library's Scan on a folder just added) reads only that
// folder and leaves every other folder's review rows alone; a folder that isn't one of the
// Movies folders is refused.
func TestScanOfOneFolder(t *testing.T) {
	svc, _, movies, kids := foldersFixture(t)
	ctx := context.Background()
	videoFixture(t, filepath.Join(movies, "Mystery Film (2001)", "Mystery.Film.2001.mkv"))
	if _, err := svc.ScanLibrary(ctx, "", ScanOptions{}); err != nil {
		t.Fatal(err)
	}
	videoFixture(t, filepath.Join(movies, "Other (2020)", "Other.2020.1080p.mkv"))
	videoFixture(t, filepath.Join(kids.Path, "Toy Story (1995)", "Toy.Story.1995.mkv"))
	videoFixture(t, filepath.Join(kids.Path, "Unknown Cartoon (1999)", "Unknown.Cartoon.1999.mkv"))

	res, err := svc.ScanLibrary(ctx, kids.Path, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 || len(res.Unmatched) != 1 || res.Unmatched[0].Root != kids.Path {
		t.Fatalf("result = %+v, want Kids' one film imported and its one unknown folder", res)
	}
	if _, err := svc.repo.GetByTMDB(ctx, 20); err == nil {
		t.Error("the scan of Kids imported a film from the main folder")
	}
	un, _ := svc.LastUnmatched(ctx)
	if len(un) != 2 {
		t.Fatalf("needs review = %+v, want the main folder's row kept and Kids' added", un)
	}

	for _, bad := range []string{t.TempDir(), filepath.Join(kids.Path, "Toy Story (1995)")} {
		if _, err := svc.ScanLibrary(ctx, bad, ScanOptions{}); err == nil || !strings.Contains(err.Error(), "isn't one of the Movies folders") {
			t.Errorf("scan of %s = %v, want it refused", bad, err)
		}
	}
}
