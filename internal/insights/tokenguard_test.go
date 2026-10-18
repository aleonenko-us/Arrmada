package insights

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The saved token goes only to the saved address; a sign-in still waiting for an address
// lets the offered servers (or, when none answered, a typed address) take it for a while.
func TestSavedTokenOnlyGoesToItsAddress(t *testing.T) {
	ctx := context.Background()
	s := newLinkService(t, "http://plex.invalid:32400")

	if !s.savedTokenMayGo(ctx, "HTTP://plex.invalid:32400/") || !s.savedTokenMayGo(ctx, "") {
		t.Fatal("the saved address (or clearing it) was refused")
	}
	if s.savedTokenMayGo(ctx, "http://evil.invalid:32400") {
		t.Fatal("a new address took the saved token")
	}
	if err := s.SetConfig(ctx, "http://evil.invalid:32400", nil, nil, nil); !errors.Is(err, ErrTokenForNewAddress) {
		t.Fatalf("SetConfig to a new address without a token: %v", err)
	}
	if _, err := s.Test(ctx, "http://evil.invalid:32400", ""); !errors.Is(err, ErrTokenForNewAddress) {
		t.Fatalf("Test of a new address without a token: %v", err)
	}

	// Several servers answered a sign-in: only those may be picked without the token.
	s.markUnbound(ctx, []ServerChoice{{URL: "http://10.0.0.5:32400"}, {URL: "https://x.plex.direct:32400"}})
	if !s.savedTokenMayGo(ctx, "http://10.0.0.5:32400") || s.savedTokenMayGo(ctx, "http://evil.invalid:32400") {
		t.Fatal("offered servers: wrong answer")
	}
	// None answered: the address typed next takes it.
	s.markUnbound(ctx, nil)
	if !s.savedTokenMayGo(ctx, "http://typed.invalid:32400") {
		t.Fatal("none answered: the typed address was refused")
	}
	// Saving an address settles it; the exception is gone.
	if err := s.SetConfig(ctx, "http://typed.invalid:32400", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if s.savedTokenMayGo(ctx, "http://evil.invalid:32400") {
		t.Fatal("the exception outlived the save")
	}
	// And it runs out.
	old, _ := json.Marshal(unboundToken{At: time.Now().Add(-unboundFor - time.Minute).Unix()})
	_ = s.settings.Set(ctx, keyUnbound, string(old))
	if s.savedTokenMayGo(ctx, "http://evil.invalid:32400") {
		t.Fatal("an expired sign-in still let the token move")
	}
}
