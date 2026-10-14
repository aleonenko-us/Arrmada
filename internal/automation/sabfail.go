package automation

import "strings"

// A usenet job SABnzbd gave up on comes with its reason in plain text (fail_message), and
// the reason decides what Arrmada does (docs/roadmap/USENET.md, decision 3):
//
//   - Something wrong with the release — articles the provider doesn't have, damage the
//     repair blocks can't cover, an archive that won't unpack or wants a password, a
//     duplicate — is the release's fault. It is blocklisted and another one searched for
//     on the next stall check, like any download that can't finish.
//   - Something wrong where SABnzbd writes — a full disk, a folder it may not write to, a
//     drive that went away — is nobody's release's fault. Another release would fail the
//     same way, so nothing is blocklisted and nothing replaced: Needs you says what to fix,
//     and the job stays in SABnzbd, failed, for a Retry once it's fixed.
//
// SABnzbd translates its reasons into its interface language, and only the English ones
// are known here. A reason that matches nothing is taken as the release's fault — SABnzbd
// fails far more jobs for missing articles than for anything else — and its raw text is
// logged so an unknown wording can be told apart; the health check warns when SABnzbd
// isn't in English.

// Usenet failure kinds.
const (
	// UsenetFailBadRelease: the release can't be completed. Blocklist it and fail over.
	UsenetFailBadRelease = "bad_release"
	// UsenetFailEnvironment: SABnzbd couldn't write the files. Fix the cause, then Retry.
	UsenetFailEnvironment = "environment"
)

// UsenetFailure is what a failed usenet job's reason means.
type UsenetFailure struct {
	Kind string // UsenetFailBadRelease | UsenetFailEnvironment
	// Reason is the cause in a few plain words ("articles are missing from your provider").
	Reason string
	// Fix is what to do about an environment failure; "" for a bad release (Arrmada deals
	// with those itself).
	Fix string
	// Known is false when the reason matched none of SABnzbd's English wordings.
	Known bool
}

// Environment reports whether the failure is where SABnzbd writes, not the release.
func (f UsenetFailure) Environment() bool { return f.Kind == UsenetFailEnvironment }

// usenetFailRule is one family of SABnzbd's reasons: any of the phrases (lowercase) found
// in the reason picks it. A phrase must never be part of "sabnzbd" itself (a bare "nzb"
// matched every reason that names the program).
type usenetFailRule struct {
	kind, reason, fix string
	phrases           []string
}

// usenetFailRules are tried in order, environment first: "Unpacking failed, write error or
// disk is full?" is a full disk, not a bad archive.
var usenetFailRules = []usenetFailRule{
	{UsenetFailEnvironment, "the disk is full",
		"Free up space on the drive SABnzbd downloads to, then Retry the download in Activity",
		[]string{"disk is full", "disk full", "no space left", "out of space", "not enough space", "not enough free space",
			"insufficient space", "errno 28", "quota exceeded"}},
	{UsenetFailEnvironment, "SABnzbd isn't allowed to write its folders",
		"Make SABnzbd's download folders writable by the user it runs as (PUID/PGID in Docker), then Retry the download in Activity",
		[]string{"permission denied", "access is denied", "access denied", "errno 13", "operation not permitted",
			"read-only file system", "errno 30"}},
	{UsenetFailEnvironment, "SABnzbd couldn't write the files",
		"Check the drive SABnzbd saves to is mounted and healthy, then Retry the download in Activity",
		[]string{"write error", "cannot write", "can't write", "could not write", "error writing", "failed writing",
			"cannot create final folder", "failed making", "moving failed", "failed moving", "i/o error", "errno 5]",
			"path is too long", "filename too long", "errno 36", "no such file or directory", "errno 2]"}},
	{UsenetFailEnvironment, "SABnzbd's post-processing script failed",
		"Check the script set for this category in SABnzbd → Config → Categories, then Retry the download in Activity",
		[]string{"script returned", "script exit", "script failed"}},

	{UsenetFailBadRelease, "it's password-protected",
		"", []string{"password", "encrypt"}},
	{UsenetFailBadRelease, "articles are missing from your provider",
		"", []string{"not on your server", "not-complete", "cannot be completed", "articles were missing", "missing articles",
			"articles missing", "of required", "out of retention", "retention", "incomplete"}},
	{UsenetFailBadRelease, "it's damaged and couldn't be repaired",
		"", []string{"repair failed", "repair blocks", "verification failed", "par2", "crc"}},
	{UsenetFailBadRelease, "it couldn't be unpacked",
		"", []string{"unpacking failed", "unpack failed", "unrar", "7zip", "7z ", "archive"}},
	{UsenetFailBadRelease, "it's a duplicate of a download SABnzbd already had",
		"", []string{"duplicate"}},
	{UsenetFailBadRelease, "it contains files SABnzbd refuses",
		"", []string{"unwanted extension", "blocked extension", "extension"}},
	{UsenetFailBadRelease, "SABnzbd couldn't read the NZB",
		"", []string{"failed to fetch", "invalid nzb", "empty nzb", "nzb is empty", "corrupt nzb"}},
	{UsenetFailBadRelease, "SABnzbd aborted it",
		"", []string{"aborted"}},
}

// ClassifyUsenetFailure reads a failed usenet job's reason (download.Item.FailMessage).
func ClassifyUsenetFailure(msg string) UsenetFailure {
	low := strings.ToLower(msg)
	for _, r := range usenetFailRules {
		for _, p := range r.phrases {
			if strings.Contains(low, p) {
				return UsenetFailure{Kind: r.kind, Reason: r.reason, Fix: r.fix, Known: true}
			}
		}
	}
	return UsenetFailure{Kind: UsenetFailBadRelease, Reason: "SABnzbd marked it failed"}
}
