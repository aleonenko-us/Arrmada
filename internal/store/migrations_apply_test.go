package store_test

import (
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

func TestMigrationsApplyFresh(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open/migrate: %v", err)
	}
	defer st.Close()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM convert_failures`).Scan(&n); err != nil {
		t.Fatalf("convert_failures missing after 0059: %v", err)
	}
	var key string
	if err := st.DB().QueryRow(`SELECT item_key FROM convert_failures LIMIT 1`).Scan(&key); err != nil && err.Error() != "sql: no rows in result set" {
		t.Fatalf("item_key column missing: %v", err)
	}
	if _, err := st.DB().Exec(`SELECT path, media_type, video_codec FROM convert_library LIMIT 1`); err != nil {
		t.Fatalf("convert_library missing after 0058: %v", err)
	}
	// 0222: the subtitle ledger. One live row per sidecar path; a superseded row frees it.
	db := st.DB()
	for _, q := range []string{
		`INSERT INTO subtitle_files (media_key, kind, video_path, path, lang, source, created_at) VALUES ('m:1', 'movie', '/v.mkv', '/v.en.srt', 'en', 'embedded', 1)`,
		`UPDATE subtitle_files SET superseded_at = 2 WHERE path = '/v.en.srt'`,
		`INSERT INTO subtitle_files (media_key, kind, video_path, path, lang, source, created_at) VALUES ('m:1', 'movie', '/v.mkv', '/v.en.srt', 'en', 'opensubtitles', 3)`,
		`INSERT INTO subtitle_attempts (run_id, media_key, video_path, lang, rung, outcome, at) VALUES ('r', 'm:1', '/v.mkv', 'en', 'ai', 'ok', 3)`,
		`INSERT INTO subtitle_blocklist (provider, file_id, media_key, lang, reason, at) VALUES ('opensubtitles', '9', 'm:1', 'en', 'redo', 3)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("subtitle ledger after 0222: %v\n%s", err, q)
		}
	}
	if _, err := db.Exec(`INSERT INTO subtitle_files (media_key, kind, video_path, path, lang, source, created_at) VALUES ('m:1', 'movie', '/v.mkv', '/v.en.srt', 'en', 'ai-transcribe', 4)`); err == nil {
		t.Error("a second live row for the same sidecar path was accepted")
	}
	var variant string
	if err := db.QueryRow(`SELECT variant FROM subtitle_attempts`).Scan(&variant); err != nil || variant != "full" {
		t.Errorf("attempt variant = %q (%v), want the 'full' default", variant, err)
	}
}
