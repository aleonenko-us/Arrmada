package settings

// Music placement settings (MUS-33, MUS-34, MUS-36).
const (
	// KeyMusicWriteTags writes the house tag style into imported music (in a staging
	// copy; a seeding file is never written). Off keeps files exactly as downloaded:
	// hardlinked, no tag writes.
	KeyMusicWriteTags = "music_write_tags"
	// KeyMusicExtras is the allowlist of extra files (artwork, booklets, lyrics, logs)
	// brought along with an album: extensions separated by spaces.
	KeyMusicExtras = "music_extras"
	// The music naming templates.
	KeyNamingMusicFolder      = "naming_music_folder"
	KeyNamingMusicTrack       = "naming_music_track"
	KeyNamingMusicCompilation = "naming_music_compilation"
)

// MusicWriteTagsDefault is on: the owner's decision (2026-10-19).
const MusicWriteTagsDefault = true
