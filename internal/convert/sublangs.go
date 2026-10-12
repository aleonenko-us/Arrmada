package convert

import (
	"context"
	"strings"
)

// SameAsSubtitles, saved as the subtitle keep-list (convert_keep_sub_langs), means "keep the
// languages the Subtitles module keeps", so the two settings can't drift apart. It may
// stand alone or beside explicit codes ("@subtitles, ja").
const SameAsSubtitles = "@subtitles"

// SetSubtitleLangs hands Convert the Subtitles module's kept languages (main wires it;
// convert must not import subtitles). Without it, SameAsSubtitles keeps every subtitle.
func (s *Service) SetSubtitleLangs(fn func(ctx context.Context) []string) {
	s.subtitleLangs = fn
}

// keepSubLangs turns the saved subtitle keep-list into the languages a plan keeps. The
// SameAsSubtitles sentinel expands to the Subtitles languages. When those can't be read or
// come back empty, the answer is nil — keep every subtitle — never "keep nothing": an
// unanswered question must not strip a library's subtitles.
func (s *Service) keepSubLangs(ctx context.Context, saved string) []string {
	list := splitCSV(saved)
	follow, extra := false, make([]string, 0, len(list))
	for _, l := range list {
		if strings.EqualFold(l, SameAsSubtitles) {
			follow = true
			continue
		}
		extra = append(extra, l)
	}
	if !follow {
		return list
	}
	var subs []string
	if s.subtitleLangs != nil {
		subs = s.subtitleLangs(ctx)
	}
	if len(subs) == 0 {
		return nil
	}
	out := append([]string(nil), subs...)
	for _, l := range extra {
		if !langIn(l, out) {
			out = append(out, l)
		}
	}
	return out
}
