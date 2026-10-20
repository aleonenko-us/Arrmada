package health

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// MisplacedShow is a show with episode files outside its show's folder (series.MisplacedShow).
type MisplacedShow struct {
	SeriesID int64
	Title    string
	Files    int
	// NoFolder: none of its files are in a folder of its own.
	NoFolder bool
}

// maxMisplacedFindings is how many shows get a finding of their own; the rest are counted
// in one more.
const maxMisplacedFindings = 10

// MisplacedEpisodesCheck reports shows whose episode files sit outside their show's folder
// in a folder no show should own — most often <TV folder>/S13/Season 12/…, where imports
// for a show with "S13"-style season folders went before #56 was fixed. Each finding links
// to the show, whose page offers to put the files back (a preview first; nothing moves on
// its own). It reads the database only: never the disk, which could wake a sleeping array.
func MisplacedEpisodesCheck(list func(ctx context.Context) ([]MisplacedShow, error)) Check {
	return Check{
		Key: "library.series-misplaced", Name: "TV episode folders", Category: CategoryStorage,
		Interval: time.Hour,
		Run: func(ctx context.Context) []Finding {
			shows, err := list(ctx)
			if err != nil || len(shows) == 0 {
				return nil
			}
			var out []Finding
			for i, s := range shows {
				if i == maxMisplacedFindings {
					rest := 0
					for _, r := range shows[i:] {
						rest += r.Files
					}
					out = append(out, Finding{
						Key: "library.series-misplaced.more", Level: LevelWarning,
						Message: fmt.Sprintf("%d more shows have %d episode file%s outside their show's folder. Open each show to put them back.", len(shows)-i, rest, plural(rest)),
						Fix:     &Fix{Path: "/series", Label: "Series"},
					})
					break
				}
				msg := fmt.Sprintf("%s: %d episode file%s outside the show's folder, in a folder named like a season (or one other shows share). New episodes won't import until they're put back.", s.Title, s.Files, plural(s.Files))
				if !s.NoFolder {
					msg = fmt.Sprintf("%s: %d episode file%s outside the show's folder, in a folder named like a season (or one other shows share).", s.Title, s.Files, plural(s.Files))
				}
				out = append(out, Finding{
					Key: "library.series-misplaced." + strconv.FormatInt(s.SeriesID, 10), Level: LevelWarning, Message: msg,
					Fix: &Fix{Path: "/series/" + strconv.FormatInt(s.SeriesID, 10) + "?fix=misplaced", Label: "Put them back"},
				})
			}
			return out
		},
	}
}
