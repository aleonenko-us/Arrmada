package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/convert"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// A Problems group's "Try these again" is one request: ?kind= clears that kind only.
func TestConvertSkipsClearByKind(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	mv := movies.NewService(db, nil, nil, dir, "", nil, log)
	cv := convert.NewService(db, mv, nil, settings.NewService(db), filepath.Join(dir, "no-ffmpeg"), filepath.Join(dir, "no-ffprobe"),
		filepath.Join(dir, "scratch"), filepath.Join(dir, "recycle"), log)
	a := &api{deps: Deps{Store: st, Log: log, Movies: mv, Convert: cv}}
	for _, q := range []string{
		`INSERT INTO convert_skips (item_key, kind, reason, permanent) VALUES ('movie:1', 'hardlinked', 'seeding', 0)`,
		`INSERT INTO convert_skips (item_key, kind, reason, permanent) VALUES ('movie:2', 'hardlinked', 'seeding', 0)`,
		`INSERT INTO convert_skips (item_key, kind, reason, permanent) VALUES ('movie:3', 'not_smaller', 'only 5%', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	a.handleConvertSkipsClear(rec, httptest.NewRequest(http.MethodPost, "/api/v1/convert/skips/clear?kind=hardlinked", nil))
	var out struct {
		Cleared bool  `json:"cleared"`
		Count   int64 `json:"count"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.Cleared || out.Count != 2 {
		t.Fatalf("%d %s, want both seeding files cleared", rec.Code, rec.Body.String())
	}
	list, err := cv.Skips(context.Background())
	if err != nil || len(list) != 1 || list[0].Kind != "not_smaller" {
		t.Fatalf("left %+v (%v), want only the other kind", list, err)
	}

	rec = httptest.NewRecorder()
	a.handleConvertSkipsClear(rec, httptest.NewRequest(http.MethodPost, "/api/v1/convert/skips/clear", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no key, kind or all: %d, want 400", rec.Code)
	}
}
