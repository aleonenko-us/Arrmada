package attention

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// SubtitleProblems is the Subtitles module's parked work that needs a person
// (*subtitles.Service).
type SubtitleProblems interface {
	Actionable(ctx context.Context) ([]subtitles.Actionable, error)
}

type subtitlesProvider struct{ src SubtitleProblems }

// Subtitles reports, one item per reason (subtitles:<reason>), the file languages the
// Subtitles module has parked until someone acts: a rejected OpenSubtitles login, a
// whisper model to download, whisper failing over and over. A subtitle that simply
// doesn't exist yet isn't here — the sweep keeps looking on its own. The Overview's
// board lists them file by file, with the fix.
func Subtitles(src SubtitleProblems) Provider { return subtitlesProvider{src} }

func (subtitlesProvider) Name() string { return "subtitles" }

func (p subtitlesProvider) Collect(ctx context.Context, _ *Frame) ([]Item, error) {
	if p.src == nil {
		return nil, nil
	}
	acts, err := p.src.Actionable(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(acts))
	for _, a := range acts {
		it := Item{
			Key: KindSubs + ":" + a.Reason, Kind: KindSubs, Level: LevelWarning, Count: a.Count,
			Name:   subtitleWords(a.Reason),
			Title:  "Subtitles: " + subtitleWords(a.Reason) + " — " + itoa(a.Count) + " waiting",
			Detail: clip(a.Detail, 300),
			Since:  a.Since * 1000,
		}
		if a.Reason == string(subtitles.ResultAuthFailed) {
			it.Level = LevelError // nothing downloads until it's fixed
		}
		linkTo(&it, health.FixSubtitles)
		out = append(out, it)
	}
	return out, nil
}

// subtitleWords is a parked reason as the rest of a sentence.
func subtitleWords(reason string) string {
	switch subtitles.Result(reason) {
	case subtitles.ResultAuthFailed:
		return "the OpenSubtitles login was rejected"
	case subtitles.ResultModelMissing:
		return "a whisper model is needed"
	case subtitles.ResultAIFailed:
		return "the AI keeps failing"
	}
	return reason
}
