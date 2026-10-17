package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0329 is additive: existing accounts keep signing in with just their password (no
// two-factor rows), and the new rows go with their account when it's deleted.
func TestTwoFactorMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0329_two_factor" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0328: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role) VALUES (1, 'owner', 'h', 'admin'), (2, 'kid', 'h', 'requester')`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM users`); n != 2 {
		t.Errorf("users after the migration: %d", n)
	}
	if n := count(t, db, `SELECT (SELECT COUNT(*) FROM user_totp) + (SELECT COUNT(*) FROM user_recovery_codes) + (SELECT COUNT(*) FROM user_trusted_devices)`); n != 0 {
		t.Errorf("an existing account got two-factor rows: %d", n)
	}
	for _, q := range []string{
		`INSERT INTO user_totp (user_id, secret, enabled) VALUES (2, 'S', 1)`,
		`INSERT INTO user_recovery_codes (user_id, code_hash) VALUES (2, 'h')`,
		`INSERT INTO user_trusted_devices (token_hash, user_id, expires_at) VALUES ('t', 2, '2099-01-01 00:00:00')`,
		`DELETE FROM users WHERE id = 2`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if n := count(t, db, `SELECT (SELECT COUNT(*) FROM user_totp) + (SELECT COUNT(*) FROM user_recovery_codes) + (SELECT COUNT(*) FROM user_trusted_devices)`); n != 0 {
		t.Errorf("%d two-factor rows outlived their account", n)
	}
	// One secret per account.
	if _, err := db.Exec(`INSERT INTO user_totp (user_id, secret) VALUES (1, 'A')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_totp (user_id, secret) VALUES (1, 'B')`); err == nil {
		t.Error("a second secret row for one account was accepted")
	}
}
