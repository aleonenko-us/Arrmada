package automation

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// Every function that writes the grabs table must bump the version the Activity feed's
// cache is keyed on (grabsChanged); one that forgot would leave the queue showing a stale
// record for up to feedCacheMaxAge.
func TestEveryGrabsWriteBumpsTheFeedVersion(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	write := regexp.MustCompile(`(?i)\b(UPDATE grabs|INSERT INTO grabs|DELETE FROM grabs)\b`)
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			writes, bumps := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == token.STRING && write.MatchString(x.Value) {
						writes = true
					}
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "grabsChanged" {
						bumps = true
					}
				}
				return true
			})
			if writes {
				checked++
				if !bumps {
					t.Errorf("%s: %s writes grabs but never calls grabsChanged", name, fn.Name.Name)
				}
			}
		}
	}
	if checked < 10 {
		t.Errorf("only %d functions write grabs — has the SQL moved?", checked)
	}
}

func newFeedCacheCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Coordinator{db: st.DB(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// The queue's view of the record is served from the cache until a grab is written, and
// read again after one — a row slipped in behind the counter's back stays out until then.
func TestQueueLinksRebuildAfterAGrabWrite(t *testing.T) {
	c := newFeedCacheCoordinator(t)
	ctx := context.Background()
	insert := func(hash string) int64 {
		res, err := c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, seed_enabled, seed_ratio)
			VALUES (1, ?, 'x', 'movie', ?, 1, 2)`, "Rel "+hash, hash)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	first := insert("aaaa")
	links, err := c.QueueLinks(ctx)
	if err != nil || len(links.ByHash) != 1 || links.Seed["aaaa"].Ratio != 2 {
		t.Fatalf("first read: %+v, %v", links, err)
	}
	insert("bbbb") // behind the counter's back
	if links, _ := c.QueueLinks(ctx); len(links.ByHash) != 1 {
		t.Fatalf("cached read saw %d grabs, want the cached 1", len(links.ByHash))
	}
	c.setGrabStatus(ctx, first, grabStatusImported)
	links, _ = c.QueueLinks(ctx)
	if len(links.ByHash) != 2 || links.ByHash["aaaa"].Status != grabStatusImported {
		t.Errorf("after setGrabStatus: %+v, want both grabs and the new status", links.ByHash)
	}
}

// Pending reviews are cached until one is held or settled.
func TestPendingReviewsRebuildAfterAReviewChange(t *testing.T) {
	c := newFeedCacheCoordinator(t)
	ctx := context.Background()
	if list, err := c.PendingReviews(ctx); err != nil || len(list) != 0 {
		t.Fatalf("empty: %v %v", list, err)
	}
	c.addReview(ctx, Review{Hash: "cccc", Name: "Held.Film", MediaType: "movie", Reason: "looks wrong"})
	list, _ := c.PendingReviews(ctx)
	if len(list) != 1 {
		t.Fatalf("after addReview: %d reviews, want 1", len(list))
	}
	if err := c.resolveReview(ctx, list[0].ID, ResolutionDismissed); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.PendingReviews(ctx); len(list) != 0 {
		t.Errorf("after resolveReview: %d reviews, want 0", len(list))
	}
}

// The cache under concurrent reads and writes (run with -race): readers always get a
// whole answer, and the version only moves forward.
func TestFeedCacheConcurrent(t *testing.T) {
	c := newFeedCacheCoordinator(t)
	ctx := context.Background()
	res, err := c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash) VALUES (1, 'R', 'x', 'movie', 'dddd')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	before := c.WantedVersion()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := c.QueueLinks(ctx); err != nil {
					t.Error(err)
					return
				}
				_, _ = c.PendingReviews(ctx)
			}
		}()
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				st := grabStatusGrabbed
				if (i+j)%2 == 0 {
					st = grabStatusImported
				}
				c.setGrabStatus(ctx, id, st)
				c.reviewsChanged()
			}
		}(i)
	}
	wg.Wait()
	if after := c.WantedVersion(); after < before+160 {
		t.Errorf("WantedVersion moved %d, want at least 160 (80 grab writes, 80 review changes)", after-before)
	}
}
