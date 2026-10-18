//go:build unix

package books

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A FIFO or a symlink named metadata.opf is no OPF: opening a FIFO would hang the scan,
// and a symlink could point anywhere.
func TestReadOPFRefusesSpecialFiles(t *testing.T) {
	fifo := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(fifo, OPFName), 0o644); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	if _, err := ReadOPF(fifo); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FIFO: err = %v, want os.ErrNotExist (and no hang)", err)
	}
	real := filepath.Join(t.TempDir(), "elsewhere.opf")
	if err := os.WriteFile(real, []byte(calibreOPF2), 0o644); err != nil {
		t.Fatal(err)
	}
	link := t.TempDir()
	if err := os.Symlink(real, filepath.Join(link, OPFName)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOPF(link); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink: err = %v, want os.ErrNotExist", err)
	}
}
