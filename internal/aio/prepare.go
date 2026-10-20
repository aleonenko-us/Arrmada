package aio

import (
	"errors"
	"fmt"
)

// Setup is what each switched-on companion needs done before the supervisor starts it
// (cmd/arrmada-aio fills it in). A step's error means the companion mustn't start without
// it: run unseeded (SABnzbd with no page login or categories, qBittorrent with no login
// Arrmada knows) or unsafe (FlareSolverr with appdata open to its user). An error wrapped
// in Warning is one it can start with anyway, as it always has: a folder that couldn't be
// handed to a new PUID, say. A nil step does nothing.
type Setup struct {
	// CloseData closes appdata to FlareSolverr's user. It runs first, so the folder is
	// already PUID's when the others start writing in it.
	CloseData    func() error
	Qbittorrent  func() error
	Prowlarr     func() error
	SABnzbd      func() error
	FlareSolverr func() error // its own working folder
}

// warning marks a setup error the companion still starts after.
type warning struct{ err error }

func (w warning) Error() string { return w.err.Error() }
func (w warning) Unwrap() error { return w.err }

// Warning marks err as one the companion can start after (see Setup); nil stays nil.
func Warning(err error) error {
	if err == nil {
		return nil
	}
	return warning{err}
}

// Prepare runs each switched-on companion's setup, every one whatever happened to the
// others, and returns o with each companion whose own setup failed switched off: it's left
// out of Plan, and AppEnv tells Arrmada as if its switch were off (its client is switched
// off until a start whose setup works switches it back on). notes say what happened, for
// the container log.
func Prepare(o Options, s Setup) (Options, []string) {
	var notes []string
	run := func(name string, on *bool, step func() error) {
		if !*on || step == nil {
			return
		}
		err := step()
		var w warning
		switch {
		case err == nil:
		case errors.As(err, &w):
			notes = append(notes, fmt.Sprintf("%s: %v (starting it anyway)", name, w.err))
		default:
			notes = append(notes, fmt.Sprintf("%s: %v; not starting it", name, err))
			*on = false
		}
	}
	run("FlareSolverr", &o.FlareSolverr, s.CloseData)
	run("qBittorrent", &o.Qbittorrent, s.Qbittorrent)
	run("Prowlarr", &o.Prowlarr, s.Prowlarr)
	run("SABnzbd", &o.SABnzbd, s.SABnzbd)
	run("FlareSolverr", &o.FlareSolverr, s.FlareSolverr)
	return o, notes
}
