package attention

import (
	"context"
	"fmt"

	"github.com/tristenlammi/arrmada/internal/convert"
	"github.com/tristenlammi/arrmada/internal/health"
)

// ConvertStalls is Convert's "waiting for room" status (*convert.Service).
type ConvertStalls interface {
	Stalls(ctx context.Context) ([]convert.Stall, error)
}

type convertProvider struct{ src ConvertStalls }

// Convert reports each reason conversions are stuck waiting for disk space, one item per
// reason (convert:stalled:no_scratch): nothing converts until someone frees room, and the
// Convert page is the only other place that says so. Its alert is convert.stalled.
func Convert(src ConvertStalls) Provider { return convertProvider{src} }

func (convertProvider) Name() string { return "convert" }

func (p convertProvider) Collect(ctx context.Context, _ *Frame) ([]Item, error) {
	if p.src == nil {
		return nil, nil
	}
	stalls, err := p.src.Stalls(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(stalls))
	for _, st := range stalls {
		it := Item{
			Key: KindConvert + ":stalled:" + st.Reason, Kind: KindConvert, Level: LevelWarning,
			Name:   stallWords(st.Reason),
			Title:  "Convert is stuck: " + stallWords(st.Reason),
			Detail: stallDetail(st),
		}
		if !st.Since.IsZero() {
			it.Since = st.Since.UnixMilli()
		}
		linkTo(&it, health.FixConvertProblems)
		out = append(out, it)
	}
	return out, nil
}

// stallWords is a stall reason as the rest of a sentence.
func stallWords(reason string) string {
	switch reason {
	case convert.SkipNoScratch:
		return "the scratch folder is full"
	case convert.SkipLibraryFull:
		return "the library disk is full"
	case convert.SkipBinFull:
		return "originals don't fit in the recycle bin"
	case "hold_full":
		return "the originals hold is full"
	}
	return "waiting for disk space"
}

func stallDetail(st convert.Stall) string {
	what := st.Title
	if st.Files > 1 {
		what = fmt.Sprintf("%s and %d more", st.Title, st.Files-1)
	}
	if st.Note == "" {
		return what
	}
	return clip(what+": "+st.Note, 300)
}
