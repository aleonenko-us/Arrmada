package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/tristenlammi/arrmada/internal/movies"
)

// The rename preview lists the track old → new without moving anything; posting the
// previewed item back moves it.
func TestMovieRenamePreviewThenApply(t *testing.T) {
	s, mgr, id, video := movieDeleteServer(t, t.TempDir())
	rec := s.do("GET", fmt.Sprintf("/api/v1/movies/%d/rename", id), mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: HTTP %d: %s", rec.Code, rec.Body)
	}
	var preview struct {
		Items   []movies.RenameItem `json:"items"`
		Matches bool                `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	// "Heat (1995).mkv" has no quality in its name, so the default scheme's
	// "{title} ({year}) - {quality}" renders it unchanged.
	if len(preview.Items) != 1 || preview.Items[0].Current != video {
		t.Fatalf("items = %+v", preview.Items)
	}
	if preview.Items[0].Status != movies.RenameSame || !preview.Matches {
		t.Fatalf("item = %+v, matches = %v; want already named", preview.Items[0], preview.Matches)
	}

	// Give it a name that isn't the scheme's, then preview and apply.
	misnamed := video[:len(video)-len("Heat (1995).mkv")] + "heat.1995.1080p.bluray.mkv"
	if err := os.Rename(video, misnamed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.DB().Exec(`UPDATE movies SET movie_file_path = ? WHERE id = ?`, misnamed, id); err != nil {
		t.Fatal(err)
	}
	rec = s.do("GET", fmt.Sprintf("/api/v1/movies/%d/rename", id), mgr)
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Status != movies.RenameOK {
		t.Fatalf("items = %+v, want one ok rename", preview.Items)
	}
	body, _ := json.Marshal(map[string]any{"items": preview.Items})
	rec = s.doJSON("POST", fmt.Sprintf("/api/v1/movies/%d/rename", id), mgr, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: HTTP %d: %s", rec.Code, rec.Body)
	}
	var reply struct {
		Renamed int `json:"renamed"`
		Skipped int `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Renamed != 1 || reply.Skipped != 0 {
		t.Fatalf("reply = %+v", reply)
	}
	if _, err := os.Stat(preview.Items[0].Proposed); err != nil {
		t.Errorf("the file isn't at its new name: %v", err)
	}
}
