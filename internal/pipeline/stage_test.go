package pipeline

import (
	"strings"
	"testing"
	"time"
)

func TestDescribeEveryStage(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	done := func(in Input) Input {
		in.Complete, in.Now = true, now
		return in
	}
	cases := []struct {
		name  string
		in    Input
		stage string
		why   string // a substring the reason must hold ("" = no reason)
		group string
	}{
		{"downloading", Input{Phase: "downloading"}, Downloading, "", GroupDownloading},
		{"queued for a slot", Input{Phase: "queued"}, Queued, "download slot", GroupDownloading},
		{"fetching the file list", Input{Phase: "metadata"}, FetchingMetadata, "file list", GroupDownloading},
		// Stalled is the queue's verdict (automation.QueueStall), not the client's word for
		// this moment: qBittorrent's stalledDL inside the grace is still downloading.
		{"stalled", Input{Phase: "stalled", Stalled: true}, Stalled, "Nobody is sending", GroupDownloading},
		{"no data for a moment", Input{Phase: "stalled"}, Downloading, "Waiting for peers", GroupDownloading},
		{"no progress while called downloading", Input{Phase: "downloading", Stalled: true}, Stalled, "Nobody is sending", GroupDownloading},
		{"file list never came", Input{Phase: "metadata", Stalled: true}, Stalled, "file list", GroupDownloading},
		{"paused by hand", Input{Phase: "paused"}, Paused, "", GroupDownloading},
		{"paused by the disk guard", Input{Phase: "paused", HeldByGuard: true}, PausedByGuard, "disk guard", GroupDownloading},
		{"errored", Input{Phase: "error"}, Failed, "error", GroupDownloading},
		{"checking counts as downloading", Input{Phase: "checking"}, Downloading, "", GroupDownloading},

		{"imported and sharing", done(Input{Phase: "seeding", Linked: true, Imported: true}), Seeding, "", GroupSeeding},
		{"imported, stopped", done(Input{Phase: "paused", Imported: true}), Imported, "", GroupSeeding},
		{"grab says imported", done(Input{Phase: "seeding", Linked: true, GrabStatus: "imported"}), Seeding, "", GroupSeeding},
		{"review dismissed", done(Input{Phase: "seeding", Linked: true, GrabStatus: "dismissed"}), Seeding, "", GroupSeeding},
		{"held for review: mismatch", done(Input{Phase: "seeding", Linked: true, GrabStatus: "held", Review: &Review{Code: "mismatch", ExpectedTitle: "Dune"}}), NeedsReview, "Held for review: it doesn't look like Dune", GroupFinishing},
		{"held, review unread", done(Input{Phase: "seeding", Linked: true, GrabStatus: "held"}), NeedsReview, "Held for review", GroupFinishing},
		{"held: nothing inside", done(Input{Phase: "seeding", Review: &Review{Code: "no_media"}}), NeedsReview, "nothing inside", GroupFinishing},
		{"held, being imported by hand", done(Input{Phase: "seeding", Linked: true, GrabStatus: "held", Review: &Review{Code: "mismatch", Importing: true, Progress: "Copying… 1.0 of 4.0 GB"}}), Importing, "Importing — chosen in Needs you: Copying… 1.0 of 4.0 GB", GroupFinishing},
		{"not grabbed, being imported by hand", done(Input{Phase: "seeding", Review: &Review{Importing: true}}), Importing, "Importing — chosen in Needs you", GroupFinishing},
		{"import retrying", done(Input{Phase: "seeding", Linked: true, Failure: &Failure{Attempts: 3, LastErr: "permission denied", NextRetry: now.Add(8 * time.Minute)}}), ImportRetrying, "Import failed 3× (permission denied) — next try in 8 min", GroupFinishing},
		{"import retrying, next check", done(Input{Phase: "seeding", Failure: &Failure{Attempts: 1}}), ImportRetrying, "on the next import check", GroupFinishing},
		{"wrong category", done(Input{Phase: "seeding", WrongCategory: true, Category: "arrmada"}), WrongCategory, "In category “arrmada” — it won't import as TV", GroupFinishing},
		{"wrong category, none", done(Input{Phase: "seeding", WrongCategory: true}), WrongCategory, "In no category", GroupFinishing},
		{"grabbed, waiting for import", done(Input{Phase: "seeding", Linked: true, GrabStatus: "grabbed"}), Importing, "next import check", GroupFinishing},
		{"not grabbed, in our category", done(Input{Phase: "seeding", OwnCategory: true}), Downloaded, "matches a title", GroupFinishing},
		{"nobody's", done(Input{Phase: "seeding"}), NotManaged, "Not managed by Arrmada", GroupSeeding},

		// Usenet: unpacking is finishing, never stuck; a failure says SABnzbd's reason and
		// what happens next; a finished, imported job is Imported — it never seeds.
		{"usenet unpacking", Input{Phase: "unpacking", Usenet: true, Linked: true, GrabStatus: "grabbed"}, Unpacking, "SABnzbd is checking and unpacking it", GroupFinishing},
		{"usenet waiting its turn", Input{Phase: "queued", Usenet: true}, Queued, "Waiting its turn in SABnzbd", GroupDownloading},
		{"usenet downloading", Input{Phase: "downloading", Usenet: true}, Downloading, "", GroupDownloading},
		{"usenet failed, ours", Input{Phase: "error", Usenet: true, Linked: true, GrabStatus: "grabbed", FailMessage: "Download failed - Not on your server(s)"}, Failed, "SABnzbd failed it: Download failed - Not on your server(s) — Arrmada blocklists it and tries another release", GroupDownloading},
		{"usenet failed, disk full", Input{Phase: "error", Usenet: true, Linked: true, GrabStatus: "grabbed", FailMessage: "Unpacking failed, write error or disk is full?", FailFix: "Free up space, then Retry"}, Failed, "write error or disk is full? — Free up space, then Retry", GroupDownloading},
		{"usenet failed, not ours", Input{Phase: "error", Usenet: true, FailMessage: "Duplicate NZB"}, Failed, "SABnzbd failed it: Duplicate NZB", GroupDownloading},
		{"usenet finished, waiting for import", done(Input{Phase: "completed", Usenet: true, Linked: true, GrabStatus: "grabbed"}), Importing, "next import check", GroupFinishing},
		{"usenet imported", done(Input{Phase: "completed", Usenet: true, Linked: true, GrabStatus: "imported"}), Imported, "", GroupSeeding},
		{"usenet imported, by the record", done(Input{Phase: "completed", Usenet: true, Imported: true}), Imported, "", GroupSeeding},
	}
	for _, c := range cases {
		stage, why := Describe(c.in)
		if stage != c.stage {
			t.Errorf("%s: stage %q, want %q (why %q)", c.name, stage, c.stage, why)
		}
		if c.why == "" && why != "" {
			t.Errorf("%s: why %q, want none", c.name, why)
		}
		if !strings.Contains(why, c.why) {
			t.Errorf("%s: why %q, want it to say %q", c.name, why, c.why)
		}
		if g := Group(stage); g != c.group {
			t.Errorf("%s: group %q, want %q", c.name, g, c.group)
		}
	}
}

// A requester sees the plain subset: everything admin-only reads as a stage they know.
func TestRequesterNeverSeesAdminStages(t *testing.T) {
	plain := map[string]bool{Queued: true, Downloading: true, Paused: true, Failed: true, Importing: true, Imported: true, Seeding: true, NotManaged: true, Removed: true}
	for _, s := range []string{Queued, FetchingMetadata, Downloading, Stalled, Paused, PausedByGuard, Failed, Unpacking, Downloaded, Importing, ImportRetrying, NeedsReview, WrongCategory, Imported, Seeding, NotManaged, Removed} {
		if r := Requester(s); !plain[r] {
			t.Errorf("Requester(%q) = %q, an admin stage", s, r)
		}
	}
	// Unpacking is the download nearly done: "importing" to a requester.
	for _, s := range []string{NeedsReview, ImportRetrying, WrongCategory, Unpacking} {
		if Requester(s) != Importing {
			t.Errorf("Requester(%q) = %q, want importing", s, Requester(s))
		}
	}
}

func TestFailureReasonIsTrimmed(t *testing.T) {
	_, why := Describe(Input{Complete: true, Failure: &Failure{Attempts: 2, LastErr: strings.Repeat("é", 400)}})
	if n := len([]rune(why)); n > 220 {
		t.Errorf("why is %d runes long", n)
	}
	if strings.ContainsRune(why, '�') {
		t.Error("trimming split a character")
	}
}
