package convert

import (
	"context"
	"reflect"
	"testing"
)

func keptLangs(mi *MediaInfo, plan Plan) []string {
	var out []string
	for _, s := range keptSubs(mi, plan) {
		out = append(out, s.Lang)
	}
	return out
}

// With the keep-list set to "Same as Subtitles", a planned conversion keeps exactly the
// Subtitles languages (a regional one keeping its base language's tracks).
func TestConvertKeepSubsFollowsSubtitles(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	set(t, s, map[string]string{keyKeepSubLangs: SameAsSubtitles})
	asked := 0
	s.SetSubtitleLangs(func(context.Context) []string { asked++; return []string{"en", "pt-br"} })

	p := s.prefs(ctx)
	if !reflect.DeepEqual(p.keepSubs, []string{"en", "pt-br"}) {
		t.Fatalf("keepSubs = %v, want the Subtitles languages", p.keepSubs)
	}
	if asked == 0 {
		t.Error("the Subtitles languages were never read")
	}
	mi := withSubs(film("h264", 1920, 1080, 12000), srt("eng"), srt("por"), srt("fre"), srt("jpn"))
	if got := keptLangs(mi, p.basePlan()); !reflect.DeepEqual(got, []string{"eng", "por"}) {
		t.Errorf("kept %v, want eng and por", got)
	}
	// The settings page shows the choice, not its expansion.
	if got := s.GetSettings(ctx).KeepSubLangs; got != SameAsSubtitles {
		t.Errorf("KeepSubLangs = %q, want %q", got, SameAsSubtitles)
	}
	// Extra codes beside the sentinel are kept too.
	set(t, s, map[string]string{keyKeepSubLangs: SameAsSubtitles + ", ja, en"})
	if got := s.prefs(ctx).keepSubs; !reflect.DeepEqual(got, []string{"en", "pt-br", "ja"}) {
		t.Errorf("keepSubs = %v, want [en pt-br ja]", got)
	}
}

// A getter that is missing, or answers nothing, keeps every subtitle — never none.
func TestSentinelFallbackKeepsAll(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	set(t, s, map[string]string{keyKeepSubLangs: SameAsSubtitles})
	mi := withSubs(film("h264", 1920, 1080, 12000), srt("eng"), srt("fre"))

	for name, fn := range map[string]func(context.Context) []string{
		"not wired": nil,
		"empty":     func(context.Context) []string { return nil },
	} {
		s.SetSubtitleLangs(fn)
		p := s.prefs(ctx)
		if p.keepSubs != nil {
			t.Errorf("%s: keepSubs = %v, want nil (keep all)", name, p.keepSubs)
		}
		if got := keptLangs(mi, p.basePlan()); len(got) != 2 {
			t.Errorf("%s: kept %v, want every subtitle", name, got)
		}
	}
}

// An explicit list (every install before this choice existed) is untouched.
func TestExplicitKeepSubsUnchanged(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	set(t, s, map[string]string{keyKeepSubLangs: "en, fr"})
	s.SetSubtitleLangs(func(context.Context) []string { return []string{"de"} })
	if got := s.prefs(ctx).keepSubs; !reflect.DeepEqual(got, []string{"en", "fr"}) {
		t.Errorf("keepSubs = %v, want [en fr]", got)
	}
	if got := s.GetSettings(ctx).KeepSubLangs; got != "en, fr" {
		t.Errorf("KeepSubLangs = %q", got)
	}
}
