package library

// Bin is the recycle bin the importer sends replaced files to (nil when none was set),
// for the music staging jobs (MUS-33), which replace library files through the same bin.
func (im *Importer) Bin() Bin { return im.bin }
