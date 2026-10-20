package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/plexwatch"
	"github.com/tristenlammi/arrmada/internal/store"
)

type ownerAccess struct{}

func (ownerAccess) WatchlistAccess(context.Context) (string, string, string) {
	return "client", "token", "42"
}

// The watchlist is the owner's: an admin sees it while no account is linked to the
// owner's Plex account; once one is, only that account does. Nobody else can switch its
// automatic requests.
func TestWatchlistViewer(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	authSvc := auth.NewService(st.DB())
	admin, err := authSvc.CreateUser(ctx, "admin", "password123", auth.RoleAdmin, true)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := authSvc.CreateUser(ctx, "kid", "password123", auth.RoleRequester, false)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := authSvc.CreateUser(ctx, "owner", "password123", auth.RoleRequester, false)
	if err != nil {
		t.Fatal(err)
	}
	a := &api{deps: Deps{Auth: authSvc, PlexWatchlist: plexwatch.New(plexwatch.Deps{DB: st.DB(), Access: ownerAccess{}}),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}

	if !a.watchlistViewer(ctx, admin) || a.watchlistViewer(ctx, kid) || a.watchlistViewer(ctx, owner) {
		t.Error("before a link: only the admin should see it")
	}
	if err := authSvc.LinkPlex(ctx, owner.ID, "42", "ownerplex"); err != nil {
		t.Fatal(err)
	}
	if a.watchlistViewer(ctx, admin) || a.watchlistViewer(ctx, kid) || !a.watchlistViewer(ctx, owner) {
		t.Error("after the owner's link: only the owner's account should see it")
	}
	if rec := call(a.handlePlexWatchlistAuto, kid, "PUT", "/api/v1/discover/plex-watchlist/auto", `{"on":true}`, nil); rec.Code != http.StatusForbidden {
		t.Errorf("someone else switched it: %d", rec.Code)
	}
	// Nobody who can't see it gets a row (an empty one, not an error).
	rec := call(a.handlePlexWatchlist, kid, "GET", "/api/v1/discover/plex-watchlist", "", nil)
	if rec.Code != http.StatusOK || len(itemIDs(t, rec)) != 0 {
		t.Errorf("kid's watchlist row: %d %s", rec.Code, rec.Body)
	}
}
