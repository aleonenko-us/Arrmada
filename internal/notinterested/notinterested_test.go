package notinterested

import (
	"context"
	"errors"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

func newStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var ids []int64
	for _, name := range []string{"ann", "bob"} {
		res, err := st.DB().Exec(`INSERT INTO users (username, password_hash, role) VALUES (?, 'x', 'requester')`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	return New(st.DB()), ids[0], ids[1]
}

// Hiding is per person, idempotent, listed newest first, and undone by Restore.
func TestHideRestore(t *testing.T) {
	s, ann, bob := newStore(t)
	ctx := context.Background()
	for _, tt := range []Title{
		{MediaType: "movie", TMDBID: 603, Title: "The Matrix", Year: 1999, PosterURL: "https://image.tmdb.org/t/p/w500/m.jpg"},
		{MediaType: "series", TMDBID: 1396, Title: "Breaking Bad", PosterURL: "https://evil.example/x.jpg"},
		{MediaType: "movie", TMDBID: 603, Title: "The Matrix", Year: 1999},
	} {
		if err := s.Hide(ctx, ann, tt); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := s.Keys(ctx, ann)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || !keys["movie:603"] || !keys["series:1396"] {
		t.Errorf("keys = %v", keys)
	}
	if k, _ := s.Keys(ctx, bob); len(k) != 0 {
		t.Errorf("bob sees ann's hidden titles: %v", k)
	}
	list, _ := s.List(ctx, ann)
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	for _, l := range list {
		if l.TMDBID == 1396 && l.PosterURL != "" {
			t.Errorf("a poster off TMDB's image host was kept: %q", l.PosterURL)
		}
	}
	if err := s.Restore(ctx, ann, "movie", 603); err != nil {
		t.Fatal(err)
	}
	if k, _ := s.Keys(ctx, ann); len(k) != 1 || k["movie:603"] {
		t.Errorf("after restore keys = %v", k)
	}
	for _, bad := range []Title{{MediaType: "book", TMDBID: 1}, {MediaType: "movie"}} {
		if err := s.Hide(ctx, ann, bad); !errors.Is(err, ErrBadTitle) {
			t.Errorf("Hide(%+v) = %v, want ErrBadTitle", bad, err)
		}
	}
}
