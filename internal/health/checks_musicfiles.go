package health

import "context"

// MusicFilesCheck reports the music file check (MUS-15) refusing to mark anything because
// the music folder looked unmounted (missing, empty, or a large share of its files gone at
// once). warning is that check's last verdict, held in memory ("" = fine), so this never
// touches the disk itself, as MovieFilesCheck doesn't.
func MusicFilesCheck(warning func() string) Check {
	return Check{
		Key: "library.music-files", Name: "Music files", Category: CategoryStorage,
		Run: func(context.Context) []Finding {
			msg := warning()
			if msg == "" {
				return nil
			}
			return []Finding{{Key: "library.music-files.unmounted", Level: LevelWarning, Message: msg, Fix: FixLibraryFolders}}
		},
	}
}
