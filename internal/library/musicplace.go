package library

// Bin is the recycle bin the importer sends replaced files to (nil when none was set),
// for the music staging jobs (MUS-33), which replace library files through the same bin.
func (im *Importer) Bin() Bin { return im.bin }

// CheckMusicRoot reports whether the music folder is set and there, as every placement
// checks before it writes: staging inside a folder that isn't mounted would fill the
// container's own disk.
func (im *Importer) CheckMusicRoot() error { return im.checkRoot(im.MusicDir()) }
