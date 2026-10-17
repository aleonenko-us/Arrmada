package library

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// What ProbeFolder found.
const (
	FolderOK      = "ok"      // there, with something in it
	FolderEmpty   = "empty"   // there, but nothing besides hidden folders: new, or a share that isn't mounted
	FolderMissing = "missing" // not there (or not a folder)
	FolderUnknown = "unknown" // didn't answer in time: a dead hard-mounted share blocks every look forever
)

// ProbeTimeout is how long a page waits on a library folder before calling it unknown.
const ProbeTimeout = 2 * time.Second

// folderProbe is one look at a folder in progress; done closes when it ends.
type folderProbe struct {
	done    chan struct{}
	state   string
	entries []os.DirEntry
}

var probes = struct {
	sync.Mutex
	m map[string]*folderProbe
}{m: map[string]*folderProbe{}}

// lookAtFolder does the looking; a test swaps it for one that hangs.
var lookAtFolder = lookAt

// ProbeFolder looks at a library folder within timeout: missing, empty-looking (see
// FolderLooksMounted) or there, with its entries but the hidden ones. A folder that
// doesn't answer in time is FolderUnknown. The look itself can't be cancelled (a stat on a
// dead share never returns), so it carries on in the background, and until it ends every
// other caller waits on that same look rather than starting another: a page reloaded
// against a dead share costs one stuck look, not one per reload.
func ProbeFolder(dir string, timeout time.Duration) (string, []os.DirEntry) {
	r := ProbeFolders([]string{dir}, timeout)
	return r[0].State, r[0].Entries
}

// Probe is one folder's answer from ProbeFolders.
type Probe struct {
	State   string
	Entries []os.DirEntry
}

// ProbeFolders looks at several folders at once, each in its own look, all within the one
// timeout: one dead share neither delays nor hides the others.
func ProbeFolders(dirs []string, timeout time.Duration) []Probe {
	started := make([]*folderProbe, len(dirs))
	for i, d := range dirs {
		started[i] = startProbe(d)
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	out := make([]Probe, len(dirs))
	expired := false
	for i, p := range started {
		if !expired {
			select {
			case <-p.done:
				out[i] = Probe{p.state, p.entries}
				continue
			case <-t.C:
				expired = true
			}
		}
		// Past the deadline: only a look that has already ended still counts.
		select {
		case <-p.done:
			out[i] = Probe{p.state, p.entries}
		default:
			out[i] = Probe{State: FolderUnknown}
		}
	}
	return out
}

// startProbe starts a look at dir, or joins the one already running.
func startProbe(dir string) *folderProbe {
	key := filepath.Clean(dir)
	probes.Lock()
	defer probes.Unlock()
	if p, running := probes.m[key]; running {
		return p
	}
	p := &folderProbe{done: make(chan struct{})}
	probes.m[key] = p
	go func() {
		defer func() {
			if recover() != nil {
				p.state, p.entries = FolderUnknown, nil
			}
			probes.Lock()
			delete(probes.m, key)
			probes.Unlock()
			close(p.done)
		}()
		p.state, p.entries = lookAtFolder(dir)
	}()
	return p
}

func lookAt(dir string) (string, []os.DirEntry) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return FolderMissing, nil
	}
	if !FolderLooksMounted(dir) {
		return FolderEmpty, nil
	}
	all, err := os.ReadDir(dir)
	if err != nil {
		return FolderMissing, nil
	}
	out := all[:0]
	for _, e := range all {
		if !SkipScanDir(e.Name()) {
			out = append(out, e)
		}
	}
	return FolderOK, out
}
