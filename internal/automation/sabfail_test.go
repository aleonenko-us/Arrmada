package automation

import "testing"

// SABnzbd's fail_message wordings, as it writes them (4.x and 5.x), each with the kind it
// must read as. The first is the one the live test against a real SABnzbd recorded.
func TestClassifyUsenetFailure(t *testing.T) {
	cases := []struct {
		msg   string
		kind  string
		known bool
	}{
		// The release can't be completed: blocklist and fail over.
		{"Download failed - Not on your server(s) - https://sabnzbd.org/not-complete", UsenetFailBadRelease, true},
		{"Download failed - Not on your server(s)", UsenetFailBadRelease, true},
		{"Aborted, cannot be completed - https://sabnzbd.org/not-complete", UsenetFailBadRelease, true},
		{"Download might fail, only 71.3% of required 99.0% available", UsenetFailBadRelease, true},
		{"Repair failed, not enough repair blocks (412 short)", UsenetFailBadRelease, true},
		{"Unpacking failed, CRC error", UsenetFailBadRelease, true},
		{"Unpacking failed, archive requires a password", UsenetFailBadRelease, true},
		{"Unpacking failed, unable to find Film.part02.rar", UsenetFailBadRelease, true},
		{"Aborted, encryption detected", UsenetFailBadRelease, true},
		{"Aborted, unwanted extension detected", UsenetFailBadRelease, true},
		{"Duplicate NZB", UsenetFailBadRelease, true},
		{"Out of retention", UsenetFailBadRelease, true},
		{"Failed to fetch NZB", UsenetFailBadRelease, true},
		{"PostProcessing was aborted (Aborted)", UsenetFailBadRelease, true},

		// Where SABnzbd writes: Needs you, never a blocklist.
		{"Unpacking failed, write error or disk is full?", UsenetFailEnvironment, true},
		{"Disk full! Queue paused", UsenetFailEnvironment, true},
		{"[Errno 28] No space left on device", UsenetFailEnvironment, true},
		{"Cannot create final folder /downloads/complete/Film (2020)", UsenetFailEnvironment, true},
		{"[Errno 13] Permission denied: '/downloads/complete/Film'", UsenetFailEnvironment, true},
		{"Failed moving /downloads/incomplete/Film to /downloads/complete/Film", UsenetFailEnvironment, true},
		{"[Errno 30] Read-only file system: '/downloads/incomplete/Film'", UsenetFailEnvironment, true},
		{"[Errno 2] No such file or directory: '/downloads/incomplete/Film/__ADMIN__'", UsenetFailEnvironment, true},
		{"Unpacking failed, path is too long", UsenetFailEnvironment, true},
		{"Script exit code is 1", UsenetFailEnvironment, true},

		// A handed-over link SABnzbd couldn't fetch (urlgrabber.py, 5.1.3): the indexer, its
		// key or its limit — never a reason to blocklist the release for good, an empty reason
		// (a page instead of an NZB) included. Only "File not on server" (the indexer's 404)
		// is the release's.
		{"URL Fetching failed; Maximum retries", UsenetFailEnvironment, true},
		{"URL Fetching failed; Unauthorized access", UsenetFailEnvironment, true},
		{"URL Fetching failed; Server could not complete request", UsenetFailEnvironment, true},
		{"URL Fetching failed; Server name does not resolve", UsenetFailEnvironment, true},
		{"URL Fetching failed; Server  uses an untrusted HTTPS certificate - https://sabnzbd.org/certificate-errors", UsenetFailEnvironment, true},
		{"URL Fetching failed; Forbidden", UsenetFailEnvironment, true},
		{"URL Fetching failed; File not on server", UsenetFailBadRelease, true},
		{"URL Fetching failed; ", UsenetFailEnvironment, true},
		{"URL Fetching failed;", UsenetFailEnvironment, true},
		{"URL Fetching failed; Unknown filetype when fetching NZB", UsenetFailEnvironment, true},
		{"Unusable NZB file", UsenetFailBadRelease, true},

		// What SABnzbd says with no reason, and a translated reason: the release's fault,
		// unrecognised.
		{"SABnzbd marked the download failed", UsenetFailBadRelease, false},
		{"Download mislukt - Niet op uw server(s)", UsenetFailBadRelease, false},
		{"Téléchargement échoué - Pas sur votre(vos) serveur(s)", UsenetFailBadRelease, false},
		{"", UsenetFailBadRelease, false},
	}
	for _, c := range cases {
		got := ClassifyUsenetFailure(c.msg)
		if got.Kind != c.kind || got.Known != c.known {
			t.Errorf("%q = %s (known %v), want %s (known %v)", c.msg, got.Kind, got.Known, c.kind, c.known)
		}
		if got.Reason == "" {
			t.Errorf("%q has no reason in words", c.msg)
		}
		if got.Environment() != (got.Fix != "") {
			t.Errorf("%q: an environment failure needs a fix to show, and only it (fix %q)", c.msg, got.Fix)
		}
		if fetch := len(c.msg) > 19 && c.msg[:19] == "URL Fetching failed"; got.Fetch != fetch {
			t.Errorf("%q: Fetch = %v, want %v", c.msg, got.Fetch, fetch)
		}
	}
}
