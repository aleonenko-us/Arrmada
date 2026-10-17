package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAPIKeyCreateListRevoke(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	u, _ := s.CreateUser(ctx, "kid@example.com", "supersecret", RoleRequester, false)
	other, _ := s.CreateUser(ctx, "aunt@example.com", "supersecret", RoleRequester, false)

	raw, k, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "  n8n  ", Scope: KeyScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, APIKeyPrefix) || len(raw) != len(APIKeyPrefix)+apiKeyBodyLen || !LooksLikeAPIKey(raw) {
		t.Fatalf("key %q hasn't the arrk_ shape", raw)
	}
	if k.Name != "n8n" || k.Hint != "arrk_…"+raw[len(raw)-4:] || k.Role != RoleRequester || k.ExpiresAt != nil {
		t.Errorf("created %+v", k)
	}
	// Only the hash is stored.
	var hash, last4 string
	if err := s.db.QueryRowContext(ctx, `SELECT key_hash, last4 FROM api_keys WHERE id = ?`, k.ID).Scan(&hash, &last4); err != nil {
		t.Fatal(err)
	}
	if hash != hashToken(raw) || strings.Contains(hash, raw[5:]) || last4 != raw[len(raw)-4:] {
		t.Errorf("stored hash %q / last4 %q", hash, last4)
	}
	// Two keys are never the same.
	raw2, _, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "two", Scope: KeyScopeRead})
	if err != nil || raw2 == raw {
		t.Fatalf("second key: %v (same: %v)", err, raw2 == raw)
	}

	list, err := s.ListAPIKeys(ctx, u.ID)
	if err != nil || len(list) != 2 || list[0].Name != "two" || list[1].Name != "n8n" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].UserID != 0 || list[0].Username != "" {
		t.Errorf("own list carries the owner: %+v", list[0])
	}
	all, err := s.ListAllAPIKeys(ctx)
	if err != nil || len(all) != 2 || all[0].Username != "kid@example.com" {
		t.Fatalf("admin list = %+v, %v", all, err)
	}

	// Another account's revoke never matches.
	if gone, err := s.RevokeAPIKey(ctx, other.ID, k.ID); err != nil || gone {
		t.Errorf("another account revoked it: %v %v", gone, err)
	}
	if gone, err := s.RevokeAPIKey(ctx, u.ID, k.ID); err != nil || !gone {
		t.Fatalf("revoke: %v %v", gone, err)
	}
	if _, err := s.ValidateAPIKey(ctx, raw, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked key validates: %v", err)
	}
	if _, err := s.ValidateAPIKey(ctx, raw2, ""); err != nil {
		t.Errorf("the other key stopped working: %v", err)
	}
	// The admin's revoke says whose it was.
	_, k2, _ := s.CreateAPIKey(ctx, other.ID, NewAPIKey{Name: "x", Scope: KeyScopeRead})
	if owner, err := s.RevokeAnyAPIKey(ctx, k2.ID); err != nil || owner != other.ID {
		t.Errorf("admin revoke = %d, %v", owner, err)
	}
	if owner, err := s.RevokeAnyAPIKey(ctx, k2.ID); err != nil || owner != 0 {
		t.Errorf("admin revoke twice = %d, %v", owner, err)
	}
}

func TestAPIKeyCreateRefuses(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	u, _ := s.CreateUser(ctx, "kid@example.com", "supersecret", RoleRequester, false)
	for _, c := range []struct {
		n    NewAPIKey
		want error
	}{
		{NewAPIKey{Name: "", Scope: KeyScopeRead}, ErrAPIKeyName},
		{NewAPIKey{Name: strings.Repeat("x", 61), Scope: KeyScopeRead}, ErrAPIKeyName},
		{NewAPIKey{Name: "line\nbreak", Scope: KeyScopeRead}, ErrAPIKeyName},
		{NewAPIKey{Name: "x", Scope: "admin"}, ErrAPIKeyScope},
		{NewAPIKey{Name: "x", Scope: KeyScopeRead, ExpiresInDays: 7}, ErrAPIKeyExpiry},
		{NewAPIKey{Name: "x", Scope: KeyScopeRead, ExpiresInDays: -1}, ErrAPIKeyExpiry},
	} {
		if _, _, err := s.CreateAPIKey(ctx, u.ID, c.n); !errors.Is(err, c.want) {
			t.Errorf("%+v: %v, want %v", c.n, err, c.want)
		}
	}
	if _, _, err := s.CreateAPIKey(ctx, 999, NewAPIKey{Name: "x", Scope: KeyScopeRead}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such account: %v", err)
	}
	_ = s.SetDisabled(ctx, u.ID, true)
	if _, _, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "x", Scope: KeyScopeRead}); !errors.Is(err, ErrAPIKeyAccount) {
		t.Errorf("disabled account: %v", err)
	}
	_ = s.SetDisabled(ctx, u.ID, false)
	for i := 0; i < MaxAPIKeysPerUser; i++ {
		if _, _, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "k", Scope: KeyScopeRead}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "one too many", Scope: KeyScopeRead}); !errors.Is(err, ErrTooManyKeys) {
		t.Errorf("over the limit: %v", err)
	}
}

func TestLooksLikeAPIKey(t *testing.T) {
	good := APIKeyPrefix + strings.Repeat("a2", apiKeyBodyLen/2)
	for k, want := range map[string]bool{
		good:                         true,
		good + "a":                   false,
		good[:len(good)-1]:           false,
		"arr_" + good[5:]:            false,
		strings.ToUpper(good):        false,
		good[:10] + "1" + good[11:]:  false, // 1 isn't base32
		good[:10] + "-" + good[11:]:  false,
		"Bearer " + good:             false,
		"eyJhbGciOiJIUzI1NiJ9.token": false,
	} {
		if got := LooksLikeAPIKey(k); got != want {
			t.Errorf("LooksLikeAPIKey(%q) = %v", k, got)
		}
	}
}

// Expiry and last use run on a fake clock: a key works until its expiry and not a second
// after, and its last use is written at most every keyUsedEvery (or when the network
// changes), not on every request.
func TestAPIKeyExpiryAndLastUse(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	day0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := day0
	s.now = func() time.Time { return clock }
	u, _ := s.CreateUser(ctx, "kid@example.com", "supersecret", RoleRequester, false)

	raw, k, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "30 days", Scope: KeyScopeRead, ExpiresInDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if k.ExpiresAt == nil || !k.ExpiresAt.Equal(day0.Add(30*24*time.Hour)) {
		t.Fatalf("expires %v", k.ExpiresAt)
	}
	lastUse := func() (time.Time, string) {
		t.Helper()
		list, err := s.ListAPIKeys(ctx, u.ID)
		if err != nil || len(list) != 1 {
			t.Fatal(err)
		}
		if list[0].LastUsedAt == nil {
			return time.Time{}, list[0].LastUsedNetwork
		}
		return *list[0].LastUsedAt, list[0].LastUsedNetwork
	}
	if at, _ := lastUse(); !at.IsZero() {
		t.Errorf("never used, but last used %v", at)
	}

	if _, err := s.ValidateAPIKey(ctx, raw, "203.0.113.50"); err != nil {
		t.Fatal(err)
	}
	if at, nw := lastUse(); !at.Equal(day0) || nw != "203.0.113.x" {
		t.Errorf("first use recorded %v from %q", at, nw)
	}
	// Five minutes on, same network: no write.
	clock = day0.Add(5 * time.Minute)
	_, _ = s.ValidateAPIKey(ctx, raw, "203.0.113.51")
	if at, _ := lastUse(); !at.Equal(day0) {
		t.Errorf("written again after five minutes: %v", at)
	}
	// Another network: written straight away.
	clock = day0.Add(6 * time.Minute)
	_, _ = s.ValidateAPIKey(ctx, raw, "192.168.1.5")
	if at, nw := lastUse(); !at.Equal(clock) || nw != "local" {
		t.Errorf("network change: %v from %q", at, nw)
	}
	// Past keyUsedEvery: written.
	clock = day0.Add(20 * time.Minute)
	_, _ = s.ValidateAPIKey(ctx, raw, "192.168.1.5")
	if at, _ := lastUse(); !at.Equal(clock) {
		t.Errorf("after twenty minutes: %v", at)
	}

	clock = day0.Add(30*24*time.Hour - time.Second)
	if _, err := s.ValidateAPIKey(ctx, raw, ""); err != nil {
		t.Errorf("a second before expiry: %v", err)
	}
	clock = day0.Add(30 * 24 * time.Hour)
	if _, err := s.ValidateAPIKey(ctx, raw, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("at expiry: %v", err)
	}
	list, _ := s.ListAPIKeys(ctx, u.ID)
	if len(list) != 1 || !list[0].Expired {
		t.Errorf("listed as %+v", list)
	}
	// An expired key doesn't count against the limit.
	for i := 0; i < MaxAPIKeysPerUser; i++ {
		if _, _, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "k", Scope: KeyScopeRead}); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
}

// A key acts as the lower of its account's current role and the role it was made under,
// and a disabled account's keys stop until it's enabled again.
func TestAPIKeyRoleCap(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	u, _ := s.CreateUser(ctx, "mgr@example.com", "supersecret", RoleManager, false)
	raw, _, err := s.CreateAPIKey(ctx, u.ID, NewAPIKey{Name: "staff", Scope: KeyScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	role := func() Role {
		t.Helper()
		use, err := s.ValidateAPIKey(ctx, raw, "")
		if err != nil {
			t.Fatal(err)
		}
		return use.User.Role
	}
	if r := role(); r != RoleManager {
		t.Errorf("as made: %s", r)
	}
	_ = s.UpdateUser(ctx, u.ID, RoleAdmin, AutoApproval{})
	if r := role(); r != RoleManager {
		t.Errorf("after a promotion: %s, want manager", r)
	}
	_ = s.UpdateUser(ctx, u.ID, RoleRequester, AutoApproval{})
	if r := role(); r != RoleRequester {
		t.Errorf("after a demotion: %s, want requester", r)
	}
	_ = s.SetDisabled(ctx, u.ID, true)
	if _, err := s.ValidateAPIKey(ctx, raw, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("disabled: %v", err)
	}
	// A role the code doesn't know fails closed.
	_ = s.SetDisabled(ctx, u.ID, false)
	if _, err := s.db.ExecContext(ctx, `UPDATE api_keys SET role = 'superuser'`); err != nil {
		t.Fatal(err)
	}
	if r := role(); r != RoleReadonly {
		t.Errorf("unknown key role: %s, want readonly", r)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE api_keys SET scope = 'everything'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateAPIKey(ctx, raw, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown scope: %v", err)
	}
	if lowerRole(RoleAdmin, RoleManager) != RoleManager || lowerRole(RoleReadonly, RoleAdmin) != RoleReadonly {
		t.Error("lowerRole")
	}
}

// Merging a duplicate revokes its keys (counted in the preview), never moves them: a key
// handed over would start acting as the other account. The target's own keys stay.
func TestMergeRevokesDuplicateKeys(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	dupKey, _, err := f.s.CreateAPIKey(ctx, f.dup.ID, NewAPIKey{Name: "dup script", Scope: KeyScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err := f.s.CreateAPIKey(ctx, f.admin.ID, NewAPIKey{Name: "owner script", Scope: KeyScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.s.MergePreview(ctx, f.admin.ID, f.dup.ID)
	if err != nil || p.APIKeys != 1 {
		t.Fatalf("preview api_keys = %d, %v", p.APIKeys, err)
	}
	if err := f.s.MergeDuplicate(ctx, f.admin.ID, f.dup.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ValidateAPIKey(ctx, dupKey, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("the duplicate's key still works: %v", err)
	}
	if use, err := f.s.ValidateAPIKey(ctx, adminKey, ""); err != nil || use.User.ID != f.admin.ID {
		t.Errorf("the target's key: %+v, %v", use, err)
	}
	if list, _ := f.s.ListAPIKeys(ctx, f.admin.ID); len(list) != 1 {
		t.Errorf("target has %d keys after the merge, want its own 1", len(list))
	}
}
