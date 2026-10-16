package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Clearing a saved Movies folder falls back to the environment's — and that fallback is
// checked against the extra folders too, so a blank save can't put the default around one.
func TestBlankDefaultIsCheckedAgainstExtraFolders(t *testing.T) {
	a, _, base := foldersAPI(t)
	elsewhere := filepath.Join(base, "elsewhere")
	nested := filepath.Join(base, "movies", "kids")
	for _, d := range []string{elsewhere, nested} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if w := putPaths(t, a, map[string]any{"movies": elsewhere}); w.Code != http.StatusOK {
		t.Fatalf("save = %d %s", w.Code, w.Body.String())
	}
	// Fine while the default is elsewhere: the env folder isn't in use.
	if w := folderCall(t, a.handleAddLibraryFolder, http.MethodPost, `{"media":"movie","path":`+jsonString(nested)+`}`, ""); w.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", w.Code, w.Body.String())
	}
	w := putPaths(t, a, map[string]any{"movies": ""})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "overlaps") {
		t.Errorf("blank save over an extra folder = %d %s", w.Code, w.Body.String())
	}
	if got := getPaths(t, a)["movies"]; got != elsewhere {
		t.Errorf("movies = %q, want it unchanged", got)
	}
}
