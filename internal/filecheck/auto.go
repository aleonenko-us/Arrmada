package filecheck

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// KeyAutoReplace is "Replace bad copies automatically" (default on): a probable cam, a
// file with no sound or silent sound, or an unreadable one is replaced without waiting for
// someone to press Find a better copy. A file in the wrong language is only flagged —
// some households keep those on purpose.
const KeyAutoReplace = "file_check_auto_replace"

// AutoReplaceDefault is on.
const AutoReplaceDefault = true

// The loop guard. A film with no good copy yet (still in cinemas, every "WEB-DL" a cam)
// would otherwise be blocklisted and searched for ever: at most one automatic replace a
// day per movie or episode, and three in all; after that Needs you says it gave up and
// leaves it to the owner.
const (
	AutoReplaceEvery = 24 * time.Hour
	MaxAutoReplaces  = 3
)

// autoCodes are the findings replaced automatically.
var autoCodes = map[string]bool{CodeSuspectedCam: true, CodeNoAudio: true, CodeSilentAudio: true, CodeUnreadable: true}

// AutoReplaceOn reports the setting.
func (s *Service) AutoReplaceOn(ctx context.Context) bool {
	if s.settings == nil {
		return AutoReplaceDefault
	}
	return s.settings.GetBool(ctx, KeyAutoReplace, AutoReplaceDefault)
}

// SetAutoReplace saves the setting.
func (s *Service) SetAutoReplace(ctx context.Context, on bool) error {
	if s.settings == nil {
		return fmt.Errorf("settings aren't available")
	}
	if err := s.settings.SetBool(ctx, KeyAutoReplace, on); err != nil {
		return err
	}
	if on {
		s.AutoReplace(ctx)
	}
	s.kickAttention()
	return nil
}

// capDecision applies the loop guard to a title's replacement history: act says replace
// now; gaveUp says the automatic limit is used up. Replacements by hand don't count.
func capDecision(hist []replacement, now time.Time) (act, gaveUp bool) {
	n := 0
	var last time.Time
	for _, r := range hist {
		if !r.Auto {
			continue
		}
		n++
		if r.At.After(last) {
			last = r.At
		}
	}
	if n >= MaxAutoReplaces {
		return false, true
	}
	if n > 0 && now.Sub(last) < AutoReplaceEvery {
		return false, false
	}
	return true, false
}

// AutoReplace replaces every bad copy the guard allows, when the setting is on: one
// replace per movie or episode, for the first of its standing findings that qualifies.
func (s *Service) AutoReplace(ctx context.Context) {
	if !s.AutoReplaceOn(ctx) {
		return
	}
	fn := s.replace.Load()
	if fn == nil || *fn == nil {
		return
	}
	list, err := s.repo.findings(ctx, true, "", 0)
	if err != nil {
		s.log.Warn("file check: couldn't read findings to replace", "err", err)
		return
	}
	done := map[string]bool{}
	for _, f := range list {
		target := f.Kind + ":" + strconv.FormatInt(mediaOf(f), 10)
		if !autoCodes[f.Code] || done[target] {
			continue
		}
		done[target] = true
		hist, err := s.repo.replacements(ctx, f.Kind, mediaOf(f))
		if err != nil {
			s.log.Warn("file check: couldn't read replacements", "title", f.Title, "err", err)
			continue
		}
		act, gaveUp := capDecision(hist, s.now())
		if gaveUp {
			if _, logged := s.gaveUp.LoadOrStore(target, true); !logged {
				s.log.Info("file check: automatic replacement gave up — leaving it to you", "title", f.Title, "problem", f.Code, "tries", MaxAutoReplaces)
			}
			continue
		}
		if !act {
			continue
		}
		if err := (*fn)(ctx, f); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("file check: couldn't replace a bad copy automatically", "title", f.Title, "problem", f.Code, "err", err)
			continue
		}
		if err := s.repo.addReplacement(ctx, f.Kind, mediaOf(f), f.Code, f.Release, true, s.now()); err != nil {
			s.log.Warn("file check: couldn't record a replacement", "title", f.Title, "err", err)
		}
		s.log.Info("file check: replacing a bad copy automatically", "title", f.Title, "problem", f.Code,
			"blocklisted", f.Release, "path", f.Path)
	}
}

// Replacement states, for the words.
const (
	StateFlagged   = ""          // found; nothing done yet
	StateReplacing = "replacing" // blocklisted (when known) and searching for another copy
	StateGaveUp    = "gave_up"   // automatic replacement used up its tries
)

// Problem is a finding with what was done about it, in words for Needs you.
type Problem struct {
	Stored
	State string `json:"state,omitempty"`
	// Text is the whole sentence: "The Matrix (1999): probably a cam — blocklisted it and
	// looking for a better copy".
	Text  string `json:"text"`
	Error bool   `json:"error,omitempty"` // worth more than a warning: automatic replacement gave up
}

func (s *Service) describe(ctx context.Context, list []Stored) ([]Problem, error) {
	auto := s.AutoReplaceOn(ctx)
	hist := map[string][]replacement{}
	out := make([]Problem, 0, len(list))
	for _, f := range list {
		k := f.Kind + ":" + strconv.FormatInt(mediaOf(f), 10)
		h, ok := hist[k]
		if !ok {
			var err error
			if h, err = s.repo.replacements(ctx, f.Kind, mediaOf(f)); err != nil {
				return nil, err
			}
			hist[k] = h
		}
		st := state(f, h, auto)
		out = append(out, Problem{Stored: f, State: st, Text: ProblemText(f, st), Error: st == StateGaveUp})
	}
	return out, nil
}

// state is what has been done about a finding: replacing once a replace was asked for
// since it was found, gave up once automatic replacement used its tries on it.
// A Find a better copy pressed after the give-up is replacing again.
func state(f Stored, hist []replacement, auto bool) string {
	var lastAuto, lastHand, last time.Time
	for _, r := range hist {
		if r.Auto && r.At.After(lastAuto) {
			lastAuto = r.At
		}
		if !r.Auto && r.At.After(lastHand) {
			lastHand = r.At
		}
		if r.At.After(last) {
			last = r.At
		}
	}
	if auto && autoCodes[f.Code] && !lastHand.After(lastAuto) {
		if _, gaveUp := capDecision(hist, time.Now()); gaveUp {
			return StateGaveUp
		}
	}
	// FirstSeen is stored to the second; a replace in the same second still counts.
	if !last.IsZero() && !last.Before(f.FirstSeen.Add(-time.Second)) {
		return StateReplacing
	}
	return StateFlagged
}

// shortWords is a finding's problem in a few words, for a sentence that goes on to say
// what was done about it.
func shortWords(code, detail string) string {
	switch code {
	case CodeSuspectedCam:
		return "probably a cam"
	case CodeNoAudio:
		return "the file has no sound"
	case CodeSilentAudio:
		return "the sound track is silent"
	case CodeUnreadable:
		return "the file is damaged or cut short"
	}
	return detail
}

// ProblemText is a finding as Needs you says it.
func ProblemText(f Stored, st string) string {
	switch st {
	case StateReplacing:
		done := "looking for a better copy"
		if f.Release != "" {
			done = "blocklisted it and looking for a better copy"
		}
		return f.Title + ": " + shortWords(f.Code, f.Detail) + " — " + done
	case StateGaveUp:
		return f.Title + ": " + shortWords(f.Code, f.Detail) + " — automatic replacement gave up after " +
			strconv.Itoa(MaxAutoReplaces) + " tries"
	}
	return f.Title + ": " + f.Detail
}
