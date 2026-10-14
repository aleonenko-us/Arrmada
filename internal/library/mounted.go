package library

import "os"

// FolderLooksMounted reports whether dir exists and has something in it besides hidden
// folders (the recycle bin is one, and an unmounted mount point can still have it if
// something wrote there). An empty or missing library folder is what an unmounted array
// or share looks like, so anything that would mark files missing checks this first.
// Reads at most a few names.
func FolderLooksMounted(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(16)
		for _, n := range names {
			if !SkipScanDir(n) {
				return true
			}
		}
		if err != nil || len(names) == 0 { // io.EOF: nothing but hidden entries
			return false
		}
	}
}
