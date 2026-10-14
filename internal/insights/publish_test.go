package insights

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/plex"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The stream events carry what alert filters key on: the Plex user id, LAN or WAN by the
// bandwidth split's test, and the item and show for autoplay suppression.
func TestPublishPayloadCarriesFilterFields(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(log)
	events, cancel := bus.Subscribe("plex.stream.started")
	defer cancel()
	svc := NewService(st.DB(), settings.NewService(st.DB()), nil, bus, log)

	remote := plex.Session{SessionKey: "9", RatingKey: "501", UserID: "42", UserName: "sam", Type: "episode",
		ShowTitle: "Severance", Title: "Hello, Ms. Cobel", Year: 2022, State: "playing", PublicIP: "8.8.8.8", Location: "wan",
		Transcoding: true, VideoDecision: "transcode", AudioDecision: "copy",
		Thumb: "/library/metadata/501/thumb/1", ShowThumb: "/library/metadata/90/thumb/2"}
	local := plex.Session{SessionKey: "10", RatingKey: "77", UserID: "43", UserName: "kim", Type: "movie",
		Title: "Dune", State: "playing", Address: "192.168.1.20", Location: "lan"}
	svc.reconcile(context.Background(), svc.plex, []plex.Session{remote, local}, time.Unix(1_700_000_000, 0))

	got := map[string]map[string]any{}
	for len(got) < 2 {
		select {
		case ev := <-events:
			d := ev.Data.(map[string]any)
			got[d["session_key"].(string)] = d
		case <-time.After(2 * time.Second):
			t.Fatalf("got %d stream events, want 2", len(got))
		}
	}
	r := got["9"]
	if r["user_id"] != "42" || r["location"] != "wan" || r["rating_key"] != "501" || r["grandparent_title"] != "Severance" ||
		r["media_type"] != "episode" || r["decision"] != "transcode" || r["title"] != "Severance · Hello, Ms. Cobel" ||
		r["thumb"] != "/library/metadata/90/thumb/2" { // the show's poster, not the episode still
		t.Errorf("remote payload = %v", r)
	}
	if l := got["10"]; l["user_id"] != "43" || l["location"] != "lan" || l["decision"] != "direct_play" {
		t.Errorf("local payload = %v", l)
	}
}
