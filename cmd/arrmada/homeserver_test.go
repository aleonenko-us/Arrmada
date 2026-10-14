package main

import (
	"context"
	"errors"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Whose Watch link comes first: Jellyfin for an account linked only to Jellyfin, Plex for
// everyone else, and Plex when the account can't be read.
func TestHomeServerFollowsTheAccountsLinks(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	a := auth.NewService(st.DB())
	home := homeServer(a)

	plain, _ := a.CreateUser(ctx, "plain@example.com", "password123", auth.RoleRequester, false)
	jfOnly, _ := a.FindOrCreateJellyfinUser(ctx, "jf-1", "kid", auth.RoleRequester, auth.AutoApproval{})
	plexOnly, _ := a.FindOrCreatePlexUser(ctx, "plex-1", "mum", auth.RoleRequester, auth.AutoApproval{})
	both, _ := a.FindOrCreatePlexUser(ctx, "plex-2", "dad", auth.RoleRequester, auth.AutoApproval{})
	if err := a.LinkJellyfin(ctx, both.ID, "jf-2", "dad"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		id   int64
		want mediaserver.Kind
	}{
		"no links":      {plain.ID, mediaserver.Plex},
		"Jellyfin only": {jfOnly.ID, mediaserver.Jellyfin},
		"Plex only":     {plexOnly.ID, mediaserver.Plex},
		"both":          {both.ID, mediaserver.Plex},
		"no such user":  {9999, mediaserver.Plex},
	} {
		if got := home(ctx, c.id); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
	if got := homeServer(failingUsers{})(ctx, jfOnly.ID); got != mediaserver.Plex {
		t.Errorf("a read error: %s, want plex", got)
	}
	if got := homeServer(nil)(ctx, jfOnly.ID); got != mediaserver.Plex {
		t.Errorf("no accounts: %s, want plex", got)
	}
}

type failingUsers struct{}

func (failingUsers) UserByID(context.Context, int64) (*auth.User, error) {
	return nil, errors.New("database is locked")
}
