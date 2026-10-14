package httpapi

import (
	"context"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/insights"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// The personal Discover rows read an account's plays on both servers: its linked Plex id
// and its linked Jellyfin id.
func TestWatchIdentitiesBothServers(t *testing.T) {
	s := newRouteServer(t, nil)
	ctx := context.Background()
	u, _ := s.user(t, "sam@example.com", auth.RoleRequester)
	a := &api{deps: s.deps}
	if ids := a.watchIdentities(ctx, u.ID); len(ids) != 0 {
		t.Fatalf("unlinked = %v", ids)
	}
	const jfID = "63273eefab23413faf91bb754c90f24d"
	if err := s.auth.LinkPlex(ctx, u.ID, "4242", "sam"); err != nil {
		t.Fatal(err)
	}
	if err := s.auth.LinkJellyfin(ctx, u.ID, jfID, "sam"); err != nil {
		t.Fatal(err)
	}
	ids := a.watchIdentities(ctx, u.ID)
	if len(ids) != 2 || ids[0] != "4242" || ids[1] != jfID {
		t.Fatalf("ids = %v", ids)
	}

	db := s.st.DB()
	for _, q := range []string{
		`INSERT INTO stream_sessions (session_key, user_id, rating_key, media_type, title, year, started_at, stopped_at) VALUES ('1', '4242', '100', 'movie', 'Heat', 1995, 100, 200)`,
		`INSERT INTO stream_sessions (server, session_key, user_id, rating_key, media_type, title, year, started_at, stopped_at) VALUES ('jellyfin', 'jellyfin:a', '` + jfID + `', 'dbc9717de8df66b09eed7dacda39fcc1', 'movie', 'Dune', 2021, 300, 400)`,
		`INSERT INTO stream_sessions (session_key, user_id, rating_key, media_type, title, year, started_at, stopped_at) VALUES ('2', '999', '101', 'movie', 'Ronin', 1998, 500, 600)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ins := insights.NewService(db, settings.NewService(db), nil, nil, s.deps.Log)
	got, err := ins.RecentlyWatchedByUser(ctx, 10, ids...)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Dune" || got[1].Title != "Heat" {
		t.Fatalf("watched = %+v, want Dune (Jellyfin) then Heat (Plex), not someone else's Ronin", got)
	}
}
