package health

import "context"

// MovieFilesCheck reports the daily missing-file check refusing to mark anything because
// the movies folder looked unmounted (missing, empty, or most of its files gone at once).
// warning is that check's last verdict, held in memory ("" = fine), so this never touches
// the disk itself: on Unraid a look at a share can spin up a sleeping array disk.
func MovieFilesCheck(warning func() string) Check {
	return Check{
		Key: "library.movie-files", Name: "Movie files", Category: CategoryStorage,
		Run: func(context.Context) []Finding {
			msg := warning()
			if msg == "" {
				return nil
			}
			return []Finding{{Key: "library.movie-files.unmounted", Level: LevelWarning, Message: msg, Fix: FixLibraryFolders}}
		},
	}
}
