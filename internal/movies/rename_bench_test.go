package movies

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/store"
)

// BenchmarkRenamePlanLibrary is the Organize preview over a 2,000-movie library where
// every film sits in its own folder (with a poster and an .nfo) under an old name, so each
// one leaves its folder and every folder check runs. It uses only what main had before the
// rename fix, so the same benchmark runs there for comparison.
func BenchmarkRenamePlanLibrary(b *testing.B) {
	const n = 2000
	top := b.TempDir()
	st, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := filepath.Join(top, "Movies")
	svc := NewService(st.DB(), nil, nil, root, filepath.Join(top, "bin"), eventbus.New(log), log)
	ctx := context.Background()
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("Film %d", i)
		dir := filepath.Join(root, fmt.Sprintf("film.%d.2001.old", i))
		video := benchFile(b, filepath.Join(dir, fmt.Sprintf("film.%d.2001.1080p.bluray.mkv", i)))
		benchFile(b, filepath.Join(dir, "poster.jpg"))
		benchFile(b, filepath.Join(dir, "movie.nfo"))
		m, err := svc.repo.Create(ctx, Movie{TMDBID: 10000 + i, Title: title, Year: 2001, Monitored: true})
		if err != nil {
			b.Fatal(err)
		}
		if err := svc.repo.SetFile(ctx, m.ID, video); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		plan, err := svc.RenamePlan(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		if len(plan) != n {
			b.Fatalf("plan has %d items, want %d", len(plan), n)
		}
	}
}

func benchFile(b *testing.B, p string) string {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		b.Fatal(err)
	}
	return p
}
