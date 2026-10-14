package insights

import (
	"context"
)

// ServerStatus is one media server as the Insights header shows it: connected or not, and
// what its monitoring is doing (the same four states as Plex's badge).
type ServerStatus struct {
	Server      string `json:"server"` // plex | jellyfin
	Name        string `json:"name,omitempty"`
	Configured  bool   `json:"configured"`
	Monitoring  bool   `json:"monitoring"` // switched on (and connected)
	Status      Status `json:"status"`
	PollSeconds int    `json:"poll_seconds"`
	LastPollAt  int64  `json:"last_poll_at,omitempty"` // unix seconds of the last answer
	LastError   string `json:"last_error,omitempty"`
}

// Servers is every media server Insights can watch, Plex first. Jellyfin is listed only
// once it's wired (SetJellyfin), connected or not.
func (s *Service) Servers(ctx context.Context) []ServerStatus {
	c := s.Config(ctx)
	h := s.PollHealth(ctx)
	out := []ServerStatus{{
		Server: ServerPlex, Name: c.ServerName, Configured: h.Configured, Monitoring: h.Monitoring,
		Status: c.Status, PollSeconds: c.PollSeconds, LastPollAt: c.LastPollAt, LastError: c.LastError,
	}}
	if w := s.jfw.Load(); w != nil {
		jh := w.svc.Health(ctx)
		cfg := w.svc.Config(ctx)
		st := ServerStatus{
			Server: ServerJellyfin, Name: cfg.ServerName, Configured: jh.Configured,
			Monitoring: s.jellyfinMonitoring(ctx), PollSeconds: cfg.PollSeconds, LastError: jh.LastErr,
		}
		if !jh.LastOKAt.IsZero() {
			st.LastPollAt = jh.LastOKAt.Unix()
		}
		st.Status = statusOf(PlexHealth{Configured: st.Configured, Monitoring: st.Monitoring,
			Unauthorized: jh.Unauthorized, Consecutive: jh.Consecutive})
		out = append(out, st)
	}
	return out
}
