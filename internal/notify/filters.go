package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Plex stream alerts used to fire for every episode a binge autoplayed and for every
// counted stall, so one evening's watching was a ping per episode. A connection now
// chooses whose streams it hears about, and whether only remote streams or only
// transcodes; and the dispatcher keeps a short memory so one sitting is one "Now
// playing" and one stalling stream is one "Buffering" per quarter hour.
//
// The memory is in RAM and starts empty after a restart. Insights resumes the streams it
// was following rather than announcing them again, so a restart doesn't undo it.

// Options is what a connection's admin chose about its messages (notifications.options).
// Nothing in it is secret.
type Options struct {
	// StreamUsers limits the Plex stream events to these Plex user ids; empty = everyone.
	StreamUsers []string `json:"stream_users,omitempty"`
	// RemoteOnly: only streams from outside the server's network.
	RemoteOnly bool `json:"remote_only,omitempty"`
	// TranscodeOnly: only streams Plex is transcoding.
	TranscodeOnly bool `json:"transcode_only,omitempty"`
	// Templates are this connection's own message text per event key (templates.go).
	Templates map[string]string `json:"templates,omitempty"`
}

const (
	eventStreamStarted = "plex.stream.started"
	eventBuffering     = "plex.buffering"

	// autoplayWindow: a stream of the same show (or film) by the same person within this
	// long of the last one is the same sitting. It slides with every episode, so a
	// five-episode evening is one alert however long it runs.
	autoplayWindow = 90 * time.Minute
	// bufferingCooldown: at most one buffering alert per person and title in this long.
	bufferingCooldown = 15 * time.Minute

	maxStreamUsers = 200
	maxUserIDLen   = 64
)

// streamEvent reports whether an event is one the stream filters apply to.
func streamEvent(key string) bool { return key == eventStreamStarted || key == eventBuffering }

// ParseOptions reads a stored options column; anything unreadable is no options.
func ParseOptions(raw string) Options {
	var o Options
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &o)
	}
	return o
}

// encode is the stored form of o.
func (o Options) encode() string {
	b, err := json.Marshal(o)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Clean checks options a client sent and returns them tidied: user ids trimmed, blank
// and repeated ones dropped. Errors say what's wrong in words.
func (o Options) Clean() (Options, error) {
	out := o
	out.StreamUsers = nil
	seen := map[string]bool{}
	for _, id := range o.StreamUsers {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if len(id) > maxUserIDLen || strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return Options{}, errors.New("a Plex user in the stream filter isn't a valid id")
		}
		seen[id] = true
		out.StreamUsers = append(out.StreamUsers, id)
	}
	if len(out.StreamUsers) > maxStreamUsers {
		return Options{}, fmt.Errorf("the stream filter takes at most %d people", maxStreamUsers)
	}
	t, err := cleanTemplates(o.Templates)
	if err != nil {
		return Options{}, err
	}
	out.Templates = t
	return out, nil
}

// streamAllowed applies a connection's stream filters to a Plex event's payload. Events
// other than the Plex stream ones always pass. A filter that can't be checked because
// the payload doesn't say (an older producer) leaves the event out rather than in: a
// "remote only" connection shouldn't hear about a stream it can't tell is remote.
func streamAllowed(o Options, key string, d map[string]any) bool {
	if !streamEvent(key) {
		return true
	}
	if len(o.StreamUsers) > 0 {
		uid := str(d, "user_id")
		found := false
		for _, want := range o.StreamUsers {
			found = found || (uid != "" && uid == want)
		}
		if !found {
			return false
		}
	}
	if o.RemoteOnly && str(d, "location") != "wan" {
		return false
	}
	if o.TranscodeOnly && str(d, "decision") != "transcode" {
		return false
	}
	return true
}

// streamMemory remembers recent Plex alerts per connection, for autoplay suppression and
// the buffering cooldown.
type streamMemory struct {
	mu   sync.Mutex
	last map[string]time.Time
	// swept is when old entries were last cleared out.
	swept time.Time
}

func newStreamMemory() *streamMemory { return &streamMemory{last: map[string]time.Time{}} }

// streamKey is who and what an event is about: the person and the show for an episode
// (so the next episode is the same sitting), the person and the item otherwise. "" when
// the payload doesn't say, and then nothing is suppressed.
func streamKey(key string, d map[string]any) string {
	uid := str(d, "user_id")
	if uid == "" {
		return ""
	}
	if key == eventStreamStarted && str(d, "media_type") == "episode" {
		if show := str(d, "grandparent_title"); show != "" {
			return uid + "\x00show\x00" + show
		}
	}
	if rk := str(d, "rating_key"); rk != "" {
		return uid + "\x00item\x00" + rk
	}
	return ""
}

// allow reports whether connection connID should get this Plex event now, and records it.
// A stream start within the autoplay window of the last one for the same person and show
// is held back, and pushes the window on (so a binge stays one alert). A buffering alert
// is held back for the cooldown after the last one that went out.
func (m *streamMemory) allow(connID int64, key string, d map[string]any, now time.Time) bool {
	if !streamEvent(key) {
		return true
	}
	sk := streamKey(key, d)
	if sk == "" {
		return true
	}
	id := fmt.Sprintf("%d\x00%s\x00%s", connID, key, sk)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweep(now)
	prev, seen := m.last[id]
	switch key {
	case eventStreamStarted:
		m.last[id] = now // every start slides the window, sent or not
		return !seen || now.Sub(prev) >= autoplayWindow
	default: // buffering
		if seen && now.Sub(prev) < bufferingCooldown {
			return false
		}
		m.last[id] = now
		return true
	}
}

// sweep drops entries too old to hold anything back, at most every few minutes, so the
// map stays the size of the streams of the last couple of hours.
func (m *streamMemory) sweep(now time.Time) {
	if now.Sub(m.swept) < 5*time.Minute {
		return
	}
	m.swept = now
	for k, at := range m.last {
		if now.Sub(at) > autoplayWindow {
			delete(m.last, k)
		}
	}
}
