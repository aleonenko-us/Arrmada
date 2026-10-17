package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeFolder(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"full/Film (2020)", "full/.arrmada-recycle", "empty/.arrmada-recycle"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if state, entries := ProbeFolder(filepath.Join(root, "full"), time.Second); state != FolderOK || len(entries) != 1 || entries[0].Name() != "Film (2020)" {
		t.Errorf("full = %s %v, want ok with the film only", state, entries)
	}
	if state, entries := ProbeFolder(filepath.Join(root, "empty"), time.Second); state != FolderEmpty || entries != nil {
		t.Errorf("empty = %s %v", state, entries)
	}
	if state, _ := ProbeFolder(filepath.Join(root, "gone"), time.Second); state != FolderMissing {
		t.Errorf("gone = %s", state)
	}
}

// A share that never answers (a dead hard mount) is unknown after the deadline, and a
// second look while the first is stuck waits on it rather than starting another.
func TestProbeFolderTimesOut(t *testing.T) {
	release := make(chan struct{})
	looks := make(chan string, 4)
	lookAtFolder = func(dir string) (string, []os.DirEntry) {
		if filepath.Base(dir) != "dead" {
			return lookAt(dir)
		}
		looks <- dir
		<-release
		return FolderOK, nil
	}
	t.Cleanup(func() { lookAtFolder = lookAt })

	base := t.TempDir()
	dir := filepath.Join(base, "dead")
	alive := filepath.Join(base, "alive")
	if err := os.MkdirAll(filepath.Join(alive, "Show"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := ProbeFolders([]string{dir, alive}, 300*time.Millisecond)
	if got[0].State != FolderUnknown || got[1].State != FolderOK || len(got[1].Entries) != 1 {
		t.Errorf("dead and alive = %+v, want unknown and ok", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("the probe waited %v", time.Since(start))
	}
	if state, _ := ProbeFolder(dir+"/", 50*time.Millisecond); state != FolderUnknown {
		t.Errorf("second look = %s, want unknown", state)
	}
	if n := len(looks); n != 1 {
		t.Errorf("%d looks started, want 1 while the first is stuck", n)
	}
	close(release) // the share comes back: the next look starts afresh and answers
	deadline := time.Now().Add(5 * time.Second)
	for {
		probes.Lock()
		_, stuck := probes.m[filepath.Clean(dir)]
		probes.Unlock()
		if !stuck || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if state, _ := ProbeFolder(dir, time.Second); state != FolderOK {
		t.Errorf("after the share came back = %s, want ok", state)
	}
}
