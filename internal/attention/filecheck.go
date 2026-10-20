package attention

import (
	"context"
	"strconv"
	"unicode"

	"github.com/tristenlammi/arrmada/internal/filecheck"
	"github.com/tristenlammi/arrmada/internal/health"
)

// FileProblems is the file check's standing findings (*filecheck.Service).
type FileProblems interface {
	Findings(ctx context.Context, all bool) ([]filecheck.Problem, error)
}

type fileCheckProvider struct{ src FileProblems }

// FileChecks reports, one item per finding (filecheck:<id>), library files that look wrong
// inside: no sound, silence, a language nobody here watches in, a probable cam, a damaged
// or cut-short file. Each says what was done about it ("blocklisted it and looking for a
// better copy"); an automatic replacement that gave up is an error. Ignored ones aren't
// listed. Needs you offers Find a better copy and Ignore on each.
func FileChecks(src FileProblems) Provider { return fileCheckProvider{src} }

func (fileCheckProvider) Name() string { return "filecheck" }

func (p fileCheckProvider) Collect(ctx context.Context, _ *Frame) ([]Item, error) {
	if p.src == nil {
		return nil, nil
	}
	list, err := p.src.Findings(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(list))
	for _, f := range list {
		it := Item{
			Key: KindFileCheck + ":" + strconv.FormatInt(f.ID, 10), Kind: KindFileCheck, Level: LevelWarning,
			Title: f.Text, Name: f.Title, ID: f.ID,
			Since: f.FirstSeen.UnixMilli(),
		}
		if f.Error {
			it.Level = LevelError
		}
		// Once something was done the title says it in short; the full finding goes below.
		if f.State != filecheck.StateFlagged {
			it.Detail = clip(capitalize(f.Detail), 300)
		}
		linkTo(&it, health.FixFileChecks)
		out = append(out, it)
	}
	return out, nil
}

// capitalize starts a sentence fragment with a capital ("probably a cam…" → "Probably…").
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
