package notify

import (
	"context"
	"testing"
	"time"
)

func streamData(user, location, decision string) map[string]any {
	return map[string]any{"user": "u" + user, "title": "Dune", "user_id": user, "rating_key": "77",
		"media_type": "movie", "location": location, "decision": decision}
}

// TestStreamFilters runs the filter matrix: whose streams, remote only, transcodes only.
func TestStreamFilters(t *testing.T) {
	cases := []struct {
		name string
		o    Options
		d    map[string]any
		want bool
	}{
		{"no filters", Options{}, streamData("1", "lan", "direct_play"), true},
		{"user listed", Options{StreamUsers: []string{"1", "2"}}, streamData("2", "lan", "direct_play"), true},
		{"user not listed", Options{StreamUsers: []string{"1"}}, streamData("2", "lan", "direct_play"), false},
		{"no user id with a user filter", Options{StreamUsers: []string{"1"}}, map[string]any{"title": "x"}, false},
		{"remote only, wan", Options{RemoteOnly: true}, streamData("1", "wan", "direct_play"), true},
		{"remote only, lan", Options{RemoteOnly: true}, streamData("1", "lan", "direct_play"), false},
		{"remote only, unknown", Options{RemoteOnly: true}, map[string]any{"title": "x"}, false},
		{"transcode only, transcode", Options{TranscodeOnly: true}, streamData("1", "lan", "transcode"), true},
		{"transcode only, direct stream", Options{TranscodeOnly: true}, streamData("1", "lan", "direct_stream"), false},
		{"all three, match", Options{StreamUsers: []string{"1"}, RemoteOnly: true, TranscodeOnly: true}, streamData("1", "wan", "transcode"), true},
		{"all three, lan", Options{StreamUsers: []string{"1"}, RemoteOnly: true, TranscodeOnly: true}, streamData("1", "lan", "transcode"), false},
		{"all three, other user", Options{StreamUsers: []string{"1"}, RemoteOnly: true, TranscodeOnly: true}, streamData("3", "wan", "transcode"), false},
	}
	for _, key := range []string{eventStreamStarted, eventBuffering} {
		for _, c := range cases {
			if got := streamAllowed(c.o, key, c.d); got != c.want {
				t.Errorf("%s / %s: allowed = %v, want %v", key, c.name, got, c.want)
			}
		}
	}
	// The filters are about Plex streams only: an import still goes out.
	if !streamAllowed(Options{RemoteOnly: true, StreamUsers: []string{"1"}}, "movie.imported", map[string]any{"title": "Dune"}) {
		t.Error("stream filters held back a non-stream event")
	}
}

// A connection filtered to remote streams gets nothing from a LAN stream; one filtered to
// a user gets only that user's — end to end through dispatch.
func TestStreamFiltersDispatch(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	remote := mustCreate(t, s, Connection{Name: "remote", URL: "ntfy://a", Events: sub(eventStreamStarted), Enabled: true, Options: Options{RemoteOnly: true}})
	sam := mustCreate(t, s, Connection{Name: "sam", URL: "ntfy://b", Events: sub(eventStreamStarted), Enabled: true, Options: Options{StreamUsers: []string{"42"}}})
	if got, _ := s.Get(ctx, remote.ID); !got.Options.RemoteOnly {
		t.Fatalf("options not stored: %+v", got.Options)
	}

	lan := streamData("7", "lan", "direct_play")
	if n, err := s.EmitOnce(ctx, eventStreamStarted, "", lan); err != nil || n != 0 {
		t.Fatalf("LAN stream by someone else queued %d (%v), want 0", n, err)
	}
	samWan := streamData("42", "wan", "direct_play")
	samWan["rating_key"] = "88"
	if n, err := s.EmitOnce(ctx, eventStreamStarted, "", samWan); err != nil || n != 2 {
		t.Fatalf("sam's remote stream queued %d (%v), want 2", n, err)
	}
	var forSam int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_deliveries WHERE connection_id = ?`, sam.ID).Scan(&forSam); err != nil || forSam != 1 {
		t.Fatalf("sam's connection has %d rows (%v), want 1", forSam, err)
	}
}

// TestAutoplaySuppression: the same person starting the next episode of the same show
// within 90 minutes is one sitting — five episodes back to back send one alert — while a
// different show, or a gap longer than the window, sends again.
func TestAutoplaySuppression(t *testing.T) {
	s, _, _ := newTestService(t)
	clk := newClock()
	useClock(s, clk)
	ctx := context.Background()
	mustCreate(t, s, Connection{Name: "a", URL: "ntfy://a", Events: sub(eventStreamStarted), Enabled: true})
	ep := func(show, rk string) map[string]any {
		return map[string]any{"user": "sam", "title": show + " · ep", "user_id": "42", "rating_key": rk,
			"media_type": "episode", "grandparent_title": show, "location": "lan", "decision": "direct_play"}
	}
	sent := 0
	for i := 0; i < 5; i++ {
		n, err := s.EmitOnce(ctx, eventStreamStarted, "", ep("Severance", string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		sent += n
		clk.advance(50 * time.Minute) // 4h10m in all, but never 90 min between episodes
	}
	if sent != 1 {
		t.Fatalf("5 back-to-back episodes sent %d alerts, want 1", sent)
	}
	if n, _ := s.EmitOnce(ctx, eventStreamStarted, "", ep("Andor", "z")); n != 1 {
		t.Fatalf("a different show sent %d, want 1", n)
	}
	// Another person watching the same show is their own sitting.
	other := ep("Severance", "q")
	other["user_id"] = "43"
	if n, _ := s.EmitOnce(ctx, eventStreamStarted, "", other); n != 1 {
		t.Fatalf("a second person sent %d, want 1", n)
	}
	clk.advance(91 * time.Minute)
	if n, _ := s.EmitOnce(ctx, eventStreamStarted, "", ep("Severance", "f")); n != 1 {
		t.Fatalf("the same show the next evening sent %d, want 1", n)
	}
}

// TestBufferingCooldown: a stream that stalls ten times in ten minutes is one alert; a
// stall after the cooldown is another.
func TestBufferingCooldown(t *testing.T) {
	s, _, _ := newTestService(t)
	clk := newClock()
	useClock(s, clk)
	ctx := context.Background()
	mustCreate(t, s, Connection{Name: "a", URL: "ntfy://a", Events: sub(eventBuffering), Enabled: true})
	mustCreate(t, s, Connection{Name: "b", URL: "ntfy://b", Events: sub(eventBuffering), Enabled: true})
	d := streamData("42", "wan", "transcode")
	sent := 0
	for i := 0; i < 10; i++ {
		n, err := s.EmitOnce(ctx, eventBuffering, "", d)
		if err != nil {
			t.Fatal(err)
		}
		sent += n
		clk.advance(time.Minute)
	}
	if sent != 2 { // one per connection
		t.Fatalf("10 stalls in 10 minutes sent %d rows, want 2 (one per connection)", sent)
	}
	clk.advance(6 * time.Minute) // 16 minutes after the first alert
	if n, _ := s.EmitOnce(ctx, eventBuffering, "", d); n != 2 {
		t.Fatalf("a stall after the cooldown sent %d, want 2", n)
	}
}

func TestOptionsClean(t *testing.T) {
	o, err := Options{StreamUsers: []string{" 42 ", "42", "", "7"}, RemoteOnly: true}.Clean()
	if err != nil || len(o.StreamUsers) != 2 || o.StreamUsers[0] != "42" || o.StreamUsers[1] != "7" || !o.RemoteOnly {
		t.Fatalf("Clean = %+v, %v", o, err)
	}
	if _, err := (Options{StreamUsers: []string{"bad\nid"}}).Clean(); err == nil {
		t.Error("a control character in a user id was accepted")
	}
	if got := ParseOptions("not json"); len(got.StreamUsers) != 0 || got.RemoteOnly {
		t.Errorf("unreadable options = %+v, want none", got)
	}
}
