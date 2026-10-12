package subtitles

import (
	"context"
	"errors"

	"github.com/tristenlammi/arrmada/internal/library"
)

// errNoBin is retireSidecar refusing because no recycle bin was wired in. Without a bin the
// only way to retire a file would be deleting it, and the module never hard-deletes on
// its own account.
var errNoBin = errors.New("no recycle bin is set up for subtitles, so nothing was removed")

// SetBin tells the module where replaced and removed sidecars go: the same per-library
// recycle bins as the videos (library.RootBins). Call it at startup.
func (s *Service) SetBin(b library.Bin) { s.bin = b }

// retireSidecar is the one way the module removes a subtitle file it is replacing or that
// the owner asked to remove: into the recycle bin, like the video's own deletes. A bin
// that is deliberately switched off follows the app-wide policy (library.RemoveToBin
// deletes then); any other bin failure returns the error and the file stays where it is,
// so the caller must abort whatever it was replacing. A file that is already gone is not
// an error. Once the file has gone, the ledger's live row for the path is superseded.
func (s *Service) retireSidecar(ctx context.Context, path string) error {
	if s.bin == nil {
		return errNoBin
	}
	if _, err := library.RemoveToBin(s.bin, path); err != nil {
		return err
	}
	if err := s.ledger.SupersedeFile(context.WithoutCancel(ctx), path); err != nil {
		s.log.Warn("subtitles: couldn't record a retired subtitle", "path", path, "err", err)
	}
	return nil
}
