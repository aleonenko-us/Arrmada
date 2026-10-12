package subtitles

import "path/filepath"

// What happened to a sidecar, for sidecarChanged.
const (
	SidecarWritten = "written" // made by the ladder (extract, download, AI) or a repair
	SidecarShifted = "shifted" // its timing was nudged
	SidecarRemoved = "removed" // retired to the recycle bin
	SidecarAdopted = "adopted" // an orphan renamed to pair with its video
)

// SetLibraryChanged tells the module who to tell when a subtitle next to a video changes
// (kind "movie" or "show", and the video's folder): Plex's partial scanner
// (plexscan.Scanner.Request), which debounces per folder and maps the path to Plex's. A
// new sidecar otherwise isn't selectable on the TV until Plex's next full scan, and
// change detection on the array's FUSE mount can't be relied on. nil = nobody. Call it at
// startup.
func (s *Service) SetLibraryChanged(fn func(kind, dir string)) {
	if fn == nil {
		s.libraryChanged.Store(nil)
		return
	}
	s.libraryChanged.Store(&fn)
}

// sidecarChanged is the one notify point for a subtitle written, shifted, removed or
// adopted beside videoPath (sidecar is its path, for the log; "" when several changed).
// kind is the job's ("movie" | "episode"). It never blocks: the scanner only queues the
// folder.
func (s *Service) sidecarChanged(kind, videoPath, sidecar, action string) {
	fn := s.libraryChanged.Load()
	if fn == nil || videoPath == "" {
		return
	}
	plexKind := "movie"
	if kind == "episode" {
		plexKind = "show"
	}
	s.log.Debug("subtitles: sidecar changed; asking Plex to rescan the folder", "action", action, "path", sidecar, "video", videoPath)
	(*fn)(plexKind, filepath.Dir(videoPath))
}
