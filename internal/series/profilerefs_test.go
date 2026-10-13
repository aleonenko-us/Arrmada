package series

import (
	"context"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// The library fit report needs only each show's profile, not the list's episode roll-ups.
func TestProfileRefs(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB().Exec(`INSERT INTO series (id, tmdb_id, title, quality_profile) VALUES (1, 1, 'Bleach', 'custom:3'), (2, 2, 'Lost', 'custom:4')`); err != nil {
		t.Fatal(err)
	}
	s := NewService(st.DB(), nil, t.TempDir(), slog.Default())
	refs, err := s.ProfileRefs(context.Background())
	if err != nil || len(refs) != 2 || refs[1] != "custom:3" || refs[2] != "custom:4" {
		t.Fatalf("refs = %v, %v", refs, err)
	}
}
