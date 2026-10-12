package automation

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/outbox"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Deleting an audiobook edition tells the audiobook catalogue to re-read the book
// (book.files_changed), and never says book.imported, which would send the requester a
// "ready" notification for a book that just lost its files.
func TestDeleteEditionPublishesFilesChanged(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := books.NewService(st.DB(), nil, log)
	c := &Coordinator{db: st.DB(), books: svc, log: log}
	box, rec := outboxFor(t, c)

	added, _ := svc.AddWorks(ctx, []metadata.BookResult{{Key: "OL1W", Title: "Dune", Author: "Frank Herbert"}}, "", true)
	if len(added) != 1 {
		t.Fatalf("added %d books", len(added))
	}
	id := added[0].ID
	audio := filepath.Join(t.TempDir(), "Frank Herbert", "Dune")
	writeSized(t, filepath.Join(audio, "01.mp3"), 10)
	writeSized(t, filepath.Join(audio, "02.mp3"), 10)
	if err := svc.MarkImported(ctx, id, books.KindAudiobook, audio, "mp3", 20, 2); err != nil {
		t.Fatal(err)
	}

	c.SetRecycleDir(filepath.Join(t.TempDir(), "bin"))
	if err := c.DeleteBookEdition(ctx, id, books.KindAudiobook); err != nil {
		t.Fatal(err)
	}
	drainOutbox(t, box)
	if n := rec.count(outbox.TopicBookFilesChanged + "/audioserver.cache"); n != 1 {
		t.Fatalf("book.files_changed ran %d times, want 1", n)
	}
	var p outbox.BookFilesChanged
	rec.last(t, outbox.TopicBookFilesChanged+"/audioserver.cache", &p)
	if p.BookID != id {
		t.Fatalf("payload = %+v", p)
	}
	for _, consumer := range []string{"requests.ready", "audioserver.cache"} {
		if n := rec.count(outbox.TopicBookImported + "/" + consumer); n != 0 {
			t.Fatalf("a delete sent book.imported to %s", consumer)
		}
	}
}
