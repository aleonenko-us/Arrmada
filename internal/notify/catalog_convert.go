package notify

import (
	"fmt"
	"strings"
)

// The Convert alerts. None has a bus topic of its own: each is emitted with EmitOnce and a
// stable dedupe key, so a restart or a repeated event never sends one twice.
//
//   - convert.failed: Convert gave up on a file (its third failure). The ConvertAlerts
//     listener (convert_digest.go) emits it from the convert.blocked bus event.
//   - convert.stalled: conversions are waiting for room (scratch, library disk, bin or
//     hold). The Needs-you feed carries it as a "convert" item and its alerter emits it
//     (internal/attention), so it's announced once per run of the condition, with the
//     feed's six-hour flap guard, and shows on the Dashboard until it clears.
//   - convert.summary: one message a day after 09:00 with what Convert did since the last
//     one (the ConvertAlerts digest). Off by default.
const (
	EventConvertFailed  = "convert.failed"
	EventConvertStalled = "convert.stalled"
	EventConvertSummary = "convert.summary"
)

// convertProblemsLink is the Convert page's Problems tab, where both problems are listed
// with a Try again.
const convertProblemsLink = "/convert?tab=problems"

func init() {
	for _, d := range []EventDef{
		{
			Key: EventConvertFailed, Group: GroupConvert, DefaultOn: true,
			Label: "Convert gave up on a file", Hint: "A file failed to convert three times and is left as it was until you try it again.",
			Format: formatConvertFailed,
		},
		{
			Key: EventConvertStalled, Group: GroupConvert, DefaultOn: true,
			Label: "Convert is stuck", Hint: "Conversions are waiting for disk space: the scratch folder, the library disk or the originals' bin is full.",
			Format: func(d map[string]any) (Message, bool) {
				// The Needs-you payload: one item, or several gathered into one message.
				return needsYouMessage(d, "Convert is stuck", "⏸", "Convert problems")
			},
		},
		{
			Key: EventConvertSummary, Group: GroupConvert,
			Label: "Daily Convert summary", Hint: "One message a morning: how many files were converted, the space freed, and any problems.",
			Format: formatConvertSummary,
		},
	} {
		Register(d)
	}
}

// formatConvertFailed: "⚠️ Convert gave up on Dune (2021) after 3 tries — <last error>".
func formatConvertFailed(d map[string]any) (Message, bool) {
	title := str(d, "title")
	if title == "" {
		return Message{}, false
	}
	body := "⚠️ Convert gave up on " + title
	if n := num(d, "count"); n > 1 {
		body += fmt.Sprintf(" after %d tries", n)
	}
	if note := strings.TrimSpace(str(d, "note")); note != "" {
		body += " — " + clipText(note, 200)
	}
	return Message{Title: "Convert gave up", Body: body, Link: convertProblemsLink}, true
}

// formatConvertSummary: "Convert: 7 files, 312 GB freed, 2 problems".
func formatConvertSummary(d map[string]any) (Message, bool) {
	files, problems := num(d, "files"), num(d, "problems")
	if files <= 0 && problems <= 0 {
		return Message{}, false
	}
	parts := []string{fmt.Sprintf("%d file%s", files, plural(int(files)))}
	if saved := num(d, "saved_bytes"); saved > 0 {
		parts = append(parts, humanSize(saved)+" freed")
	}
	if problems > 0 {
		parts = append(parts, fmt.Sprintf("%d problem%s", problems, plural(int(problems))))
	}
	link := "/convert"
	if problems > 0 {
		link = convertProblemsLink
	}
	return Message{Title: "Convert summary", Body: "🎞️ Convert: " + strings.Join(parts, ", "), Link: link}, true
}

// humanSize renders a byte count the way the Convert page does (binary units).
func humanSize(b int64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.2f TB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.0f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d B", b)
}

// clipText shortens s to at most n runes, with an ellipsis when cut.
func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
