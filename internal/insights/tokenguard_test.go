package insights

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/plex"
)

// tokenTrap is a server that records the Plex token it's sent, answering /identity as
// machine id (when set).
func tokenTrap(t *testing.T, machineID string) (*httptest.Server, *atomic.Value) {
	t.Helper()
	var seen atomic.Value
	seen.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := r.Header.Get("X-Plex-Token"); tok != "" {
			seen.Store(tok)
		}
		if r.URL.Path == "/identity" && machineID != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"` + machineID + `"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// The saved token goes only to the saved address; a sign-in still waiting for an address
// lets the offered servers take it, or (none answered) an address that says, without the
// token, that it's one of the account's own servers. Any save ends that.
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

	cands := []plex.ServerCandidate{{MachineID: "M1"}, {MachineID: "M2"}}
	// Several servers answered a sign-in: only those may be picked without the token.
	s.markUnbound(ctx, []ServerChoice{{URL: "http://10.0.0.5:32400"}, {URL: "https://x.plex.direct:32400"}}, cands)
	if !s.savedTokenMayGo(ctx, "http://10.0.0.5:32400") || s.savedTokenMayGo(ctx, "http://evil.invalid:32400") {
		t.Fatal("offered servers: wrong answer")
	}
	// None answered: only an address that says it's one of the account's servers.
	own, _ := tokenTrap(t, "M2")
	stranger, seen := tokenTrap(t, "STRANGER")
	silent, _ := tokenTrap(t, "")
	s.markUnbound(ctx, nil, cands)
	if !s.savedTokenMayGo(ctx, own.URL) {
		t.Fatal("none answered: the account's own server was refused")
	}
	if s.savedTokenMayGo(ctx, stranger.URL) || s.savedTokenMayGo(ctx, silent.URL) {
		t.Fatal("none answered: another server (or a silent one) may take the token")
	}
	if seen.Load().(string) != "" {
		t.Fatal("the token was sent while checking an address")
	}
	// Any save ends the exception.
	if err := s.SetConfig(ctx, "http://plex.invalid:32400", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if s.savedTokenMayGo(ctx, own.URL) {
		t.Fatal("the exception outlived a save")
	}
	// And it runs out.
	old, _ := json.Marshal(unboundToken{At: time.Now().Add(-unboundFor - time.Minute).Unix(), Owned: []string{"M2"}})
	_ = s.settings.Set(ctx, keyUnbound, string(old))
	if s.savedTokenMayGo(ctx, own.URL) {
		t.Fatal("an expired sign-in still let the token move")
	}
}

// Review: someone points the connection at their own server with their own token; the
// owner then signs in with Plex again. The owner's fresh token never reaches that server,
// and the address is dropped rather than polled with it.
func TestPlexResignInNeverSendsTheTokenToAPlantedAddress(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	home := fakePMS(t, "M1", "Home")
	fakePlexTV(t, fakeServer{name: "Home", id: "M1", owned: true, uris: []string{home.URL}})
	for _, machine := range []string{"", "STRANGER"} {
		evil, seen := tokenTrap(t, machine)
		own := "their-own"
		if err := s.SetConfig(ctx, evil.URL, &own, nil, nil); err != nil {
			t.Fatal(err)
		}
		res, err := s.PollPlexAuth(ctx, 77)
		if err != nil {
			t.Fatal(err)
		}
		if seen.Load().(string) == "tok" {
			t.Fatalf("machine %q: the owner's fresh token was sent to the planted address", machine)
		}
		if got := s.Config(ctx).URL; got != home.URL || res.ServerName != "Home" {
			t.Fatalf("machine %q: saved URL %q (%+v), want the owner's own server", machine, got, res)
		}
	}
}

// Review: when no server answered a sign-in, the waiting token can't be taken by just
// any address — Test with a stranger's address sends nothing.
func TestPlexNoneAnsweredWindowNeedsAnOwnedServer(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	fakePlexTV(t, fakeServer{name: "Home", id: "M1", owned: true, uris: []string{deadURL(t)}})
	if _, err := s.PollPlexAuth(ctx, 77); err != nil {
		t.Fatal(err)
	}
	evil, seen := tokenTrap(t, "STRANGER")
	if _, err := s.Test(ctx, evil.URL, ""); !errors.Is(err, ErrTokenForNewAddress) {
		t.Fatalf("Test of a stranger's address: %v", err)
	}
	if seen.Load().(string) != "" {
		t.Fatal("the waiting token reached a stranger's address")
	}
	// The owner's own server, typed in, is fine.
	home := fakePMS(t, "M1", "Home")
	if err := s.SetConfig(ctx, home.URL, nil, nil, nil); err != nil {
		t.Fatalf("typing the owned server's address: %v", err)
	}
}

// When plex.tv can't list the account's servers, nothing can be checked: the sign-in
// fails and the saved connection (and its token) are left exactly as they were.
func TestPlexSignInWithoutTheServerListChangesNothing(t *testing.T) {
	s := newLinkService(t, "http://plex.invalid:32400")
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/pins/77" {
			_, _ = w.Write([]byte(`{"id":77,"authToken":"tok"}`))
			return
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(plex.SetTVBaseForTest(srv.URL))
	if _, err := s.PollPlexAuth(ctx, 77); err == nil {
		t.Fatal("the sign-in succeeded without the server list")
	}
	if got := s.settings.Get(ctx, keyToken, ""); got != "plex-secret" {
		t.Fatalf("token = %q, want the old one kept", got)
	}
	if got := s.Config(ctx).URL; got != "http://plex.invalid:32400" {
		t.Fatalf("URL = %q", got)
	}
}
