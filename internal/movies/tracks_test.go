package movies

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// MOV-12: a file is recorded on the track it was decided for, never on a path another
// track owns. Temp dirs and synthetic files only.

func trackService(t *testing.T) (*Service, context.Context, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lib := t.TempDir()
	svc := NewService(st.DB(), nil, nil, lib, "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, context.Background(), lib
}

func touch(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(filepath.Base(p)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMarkImportedRefusesPathOwnedByOtherTrack(t *testing.T) {
	svc, ctx, lib := trackService(t)
	m, err := svc.repo.Create(ctx, Movie{TMDBID: 1, Title: "Dune", Year: 2021, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	theatrical := touch(t, filepath.Join(lib, "Dune (2021)", "Dune (2021) - 2160p BluRay.mkv"))
	if err := svc.MarkImportedTo(ctx, m.ID, 0, theatrical, "Dune.2021.2160p.BluRay", false); err != nil {
		t.Fatal(err)
	}
	v, err := svc.AddVersion(ctx, m.ID, "Second", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// The same path offered to the extra track: refused, and nothing moves.
	err = svc.MarkImportedTo(ctx, m.ID, v.ID, theatrical, "Dune.2021.2160p.BluRay", false)
	if !errors.Is(err, ErrPathOwnedByOtherTrack) {
		t.Fatalf("err = %v, want ErrPathOwnedByOtherTrack", err)
	}
	if _, err := os.Stat(theatrical); err != nil {
		t.Fatalf("the default track's file was touched: %v", err)
	}
	rows, _ := svc.VersionRows(ctx, m.ID)
	if rows[1].HasFile || rows[0].FilePath != theatrical {
		t.Fatalf("records changed: %+v", rows)
	}
}

// A file decided for an extra track lands on it even when routing by resolution would
// have sent it to the default track (a tie goes to the default).
func TestMarkImportedToTargetsDecidedTrack(t *testing.T) {
	svc, ctx, lib := trackService(t)
	m, _ := svc.repo.Create(ctx, Movie{TMDBID: 2, Title: "Heat", Year: 1995, Monitored: true})
	def := touch(t, filepath.Join(lib, "Heat (1995)", "Heat (1995) - 2160p BluRay.mkv"))
	if err := svc.MarkImportedTo(ctx, m.ID, 0, def, "Heat.1995.2160p.BluRay", false); err != nil {
		t.Fatal(err)
	}
	v, _ := svc.AddVersion(ctx, m.ID, "Director's Cut", "", "Director's Cut", true)
	dc := touch(t, filepath.Join(lib, "Heat (1995)", "Heat (1995) {edition-Director's Cut} - 2160p BluRay.mkv"))
	if err := svc.MarkImportedTo(ctx, m.ID, v.ID, dc, "Heat.1995.2160p.BluRay", false); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.VersionRows(ctx, m.ID)
	if rows[0].FilePath != def || rows[1].FilePath != dc || !rows[1].HasFile {
		t.Fatalf("tracks = %+v", rows)
	}
	if _, err := os.Stat(def); err != nil {
		t.Fatalf("the default file was removed: %v", err)
	}
}

// Undecided (-1) still routes by resolution, and now by edition too: a DC file goes to
// the DC track, a theatrical one never does.
func TestRouteVersionForEditions(t *testing.T) {
	svc, ctx, _ := trackService(t)
	versions := []Version{
		{ID: 0, IsDefault: true, Label: "Default"},
		{ID: 5, Label: "Director's Cut", Edition: "Director's Cut"},
	}
	if got := svc.routeVersionFor(ctx, versions, "2160p", "Directors Cut"); got.ID != 5 {
		t.Errorf("DC file routed to %d, want the DC track", got.ID)
	}
	if got := svc.routeVersionFor(ctx, versions, "2160p", ""); got.ID != 0 {
		t.Errorf("theatrical file routed to %d, want the default track", got.ID)
	}
	if got := svc.routeVersionFor(ctx, versions, "", "Extended"); got.ID != 0 {
		t.Errorf("an edition no track wants routed to %d, want the default", got.ID)
	}
}

func TestImportNamingDecidesTrack(t *testing.T) {
	svc, ctx, lib := trackService(t)
	m, _ := svc.repo.Create(ctx, Movie{TMDBID: 3, Title: "Blade Runner", Year: 1982, Monitored: true})
	def := touch(t, filepath.Join(lib, "Blade Runner (1982)", "Blade Runner (1982) - 2160p BluRay.mkv"))
	if err := svc.MarkImportedTo(ctx, m.ID, 0, def, "Blade.Runner.1982.2160p.BluRay", false); err != nil {
		t.Fatal(err)
	}
	dc, _ := svc.AddVersion(ctx, m.ID, "Director's Cut", "", "Director's Cut", true)

	// A grab for the DC track: that track, its edition, the default file protected.
	n, err := svc.ImportNaming(ctx, m.ID, dc.ID, "", "Blade.Runner.1982.2160p.BluRay.x265-GRP")
	if err != nil {
		t.Fatal(err)
	}
	if n.VersionID != dc.ID || n.Track.Edition != "Director's Cut" || len(n.Track.Protected) != 1 || n.Track.Protected[0] != def {
		t.Fatalf("naming = %+v", n)
	}
	target := svc.TrackTarget(m, []Version{defaultVersionRow(mustGet(t, svc, m.ID)), dc}, dc, "Blade.Runner.1982.2160p.BluRay.x265-GRP", ".mkv")
	if !strings.Contains(filepath.Base(target), "{edition-Director's Cut}") || target == def {
		t.Fatalf("DC target = %q", target)
	}

	// Undecided, theatrical release: the default track.
	if n, _ := svc.ImportNaming(ctx, m.ID, -1, "", "Blade.Runner.1982.2160p.BluRay.x265-GRP"); n.VersionID != 0 {
		t.Fatalf("theatrical routed to %d", n.VersionID)
	}
	// A replace grab for the default file goes to the default track, whatever it says.
	if n, _ := svc.ImportNaming(ctx, m.ID, -1, def, "Blade.Runner.1982.Directors.Cut.2160p.BluRay"); n.VersionID != 0 {
		t.Fatalf("replace grab routed to %d", n.VersionID)
	}
	// A track that's gone: routed afresh.
	if n, _ := svc.ImportNaming(ctx, m.ID, 999, "", "Blade.Runner.1982.Directors.Cut.2160p.BluRay"); n.VersionID != dc.ID {
		t.Fatalf("deleted track's DC grab routed to %d", n.VersionID)
	}
}

// Manual import into a named track: placed under that track's name, recorded on it.
func TestManualImportIntoTrack(t *testing.T) {
	svc, ctx, lib := trackService(t)
	m, _ := svc.repo.Create(ctx, Movie{TMDBID: 4, Title: "Aliens", Year: 1986, Monitored: true})
	def := touch(t, filepath.Join(lib, "Aliens (1986)", "Aliens (1986) - 1080p BluRay.mkv"))
	if err := svc.MarkImportedTo(ctx, m.ID, 0, def, "Aliens.1986.1080p.BluRay", false); err != nil {
		t.Fatal(err)
	}
	v, _ := svc.AddVersion(ctx, m.ID, "Special", "", "", true)
	src := touch(t, filepath.Join(t.TempDir(), "Aliens.1986.1080p.BluRay.x264-OTHER.mkv"))
	if err := svc.ManualImportTo(ctx, m.ID, v.ID, src); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.VersionRows(ctx, m.ID)
	if rows[0].FilePath != def {
		t.Fatalf("default track changed: %+v", rows[0])
	}
	if want := filepath.Join(lib, "Aliens (1986)", "Aliens (1986) - 1080p BluRay - Special.mkv"); rows[1].FilePath != want {
		t.Fatalf("track file %q, want %q", rows[1].FilePath, want)
	}
	if b, _ := os.ReadFile(def); string(b) != filepath.Base(def) {
		t.Fatal("the default file's content changed")
	}
	// A track id from another movie is refused.
	if err := svc.ManualImportTo(ctx, m.ID+100, v.ID, src); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("foreign track: err = %v", err)
	}
}

func mustGet(t *testing.T, svc *Service, id int64) Movie {
	t.Helper()
	m, err := svc.repo.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
