package library

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// A queue read asks only about the hashes in the queue: ImportedAmong answers for those
// (as stored, whatever case the client gives) and leaves the rest of the history out.
func TestImportedAmong(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := NewManager(st.DB(), t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 900; i++ { // more than one IN list's worth
		if _, err := st.DB().Exec(`INSERT INTO imports (download_hash, source_path, target_path) VALUES (?, '', '')`, fmt.Sprintf("h%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.ImportedAmong(context.Background(), []string{"H0001", "h0899", "nope", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["h0001"] || !got["h0899"] {
		t.Errorf("got %v, want h0001 and h0899 only", got)
	}
	many := make([]string, 0, 900)
	for i := 0; i < 900; i++ {
		many = append(many, fmt.Sprintf("h%04d", i))
	}
	if got, err := m.ImportedAmong(context.Background(), many); err != nil || len(got) != 900 {
		t.Errorf("900 hashes: %d found, err %v", len(got), err)
	}
}
