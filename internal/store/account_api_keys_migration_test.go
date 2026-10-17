package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0331 gives api_keys what personal API keys need (scope, role, last4, expires_at,
// last_used_network) and drops any row from the old arr_ scheme, which nothing ever
// issued and which wouldn't validate any more. Users and their sessions are untouched.
func TestAccountAPIKeysMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0331_account_api_keys" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0330: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash, role) VALUES (1, 'owner', 'x', 'admin')`,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ('s1', 1, '2099-01-01 00:00:00')`,
		`INSERT INTO api_keys (user_id, name, key_hash) VALUES (1, 'legacy', 'oldhash')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM api_keys`); n != 0 {
		t.Errorf("%d legacy keys survived", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM users`); n != 1 {
		t.Errorf("users: %d, want 1", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("sessions: %d, want 1", n)
	}
	if _, err := db.Exec(`INSERT INTO api_keys (user_id, name, key_hash, scope, role, last4, expires_at, last_used_network)
		VALUES (1, 'n8n', 'h', 'full', 'admin', 'abcd', NULL, 'local')`); err != nil {
		t.Fatalf("new columns: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM api_keys WHERE scope = 'full' AND role = 'admin' AND last4 = 'abcd' AND expires_at IS NULL`); n != 1 {
		t.Errorf("new row reads back %d times", n)
	}
	// Defaults are the least a key can do.
	if _, err := db.Exec(`INSERT INTO api_keys (user_id, name, key_hash) VALUES (1, 'bare', 'h2')`); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM api_keys WHERE key_hash = 'h2' AND scope = 'read' AND role = 'readonly'`); n != 1 {
		t.Error("defaults aren't read-only")
	}
}
