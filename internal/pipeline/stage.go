// Package pipeline is the one vocabulary for where a download has got to, shared by the
// staff Activity queue and the requester's request cards, so the two never call the same
// download by different names. Staff see every stage and the reason behind it; a
// requester sees the plain subset (Requester), never the admin detail.
package pipeline

import (
	"fmt"
	"strings"
	"time"
)

// Stages. The string values are part of the API (the downloads feed's stage, a request's
// tracking.stage) and must not change.
const (
	Queued           = "queued"            // waiting for a download slot, or for the client to start it
	FetchingMetadata = "fetching_metadata" // waiting for peers to send the file list
	Downloading      = "downloading"       // bytes are arriving
	Stalled          = "stalled"           // nobody is sending data
	Paused           = "paused"            // paused in the client
	PausedByGuard    = "paused_by_guard"   // paused by the disk guard until space frees up
	Failed           = "failed"            // the client reports an error
	Unpacking        = "unpacking"         // a usenet download being verified, repaired and unpacked
	Downloaded       = "downloaded"        // finished, not grabbed by Arrmada, in one of its categories
	Importing        = "importing"         // finished; waiting for (or in) the import
	ImportRetrying   = "import_retrying"   // the import failed and will be tried again
	NeedsReview      = "needs_review"      // held for a person to decide
	WrongCategory    = "wrong_category"    // finished in a category Arrmada never imports from
	Imported         = "imported"          // in the library; no longer sharing
	Seeding          = "seeding"           // in the library and sharing
	NotManaged       = "not_managed"       // finished; nothing in Arrmada grabbed or imported it
	Removed          = "removed"           // gone from the client
)

// Groups: how the Activity queue splits its list.
const (
	GroupDownloading = "downloading"
	GroupFinishing   = "finishing"
	GroupSeeding     = "seeding"
)

// Group is the queue section a stage belongs to: still arriving, finished but not in the
// library yet, or done and sharing. Removed has none. Unpacking is finishing: every byte is
// down, and what's left is the client's work before the import.
func Group(stage string) string {
	switch stage {
	case Queued, FetchingMetadata, Downloading, Stalled, Paused, PausedByGuard, Failed:
		return GroupDownloading
	case Unpacking, Downloaded, Importing, ImportRetrying, NeedsReview, WrongCategory:
		return GroupFinishing
	case Imported, Seeding, NotManaged:
		return GroupSeeding
	}
	return ""
}

// Requester is what a requester is shown for a stage: the admin-only ones read as the
// stage a family member understands. A held or retrying import is still "importing" to
// them — the note says it's being checked, never why.
func Requester(stage string) string {
	switch stage {
	case FetchingMetadata, Stalled:
		return Downloading // "waiting for peers"
	case PausedByGuard:
		return Paused
	case Unpacking, Downloaded, ImportRetrying, NeedsReview, WrongCategory:
		return Importing
	}
	return stage
}

// BeingChecked is the requester's note for an import a person or a retry is dealing
// with: honest that it isn't stuck, without admin detail.
const BeingChecked = "Being checked"

// Review reason codes (automation.Reason*), repeated here so the package stands alone.
const (
	reviewMismatch     = "mismatch"
	reviewUnmatched    = "unmatched"
	reviewNumbering    = "numbering"
	reviewImportFailed = "import_failed"
	reviewNoMedia      = "no_media"
)

// Review is a pending review of a finished download.
type Review struct {
	Code          string // automation.Reason*
	ExpectedTitle string // what it was grabbed for, when anything
	// Importing: a person decided it (Import anyway, Choose different…, Map files…, or
	// Import into… for a download Arrmada didn't grab — a Review with no Code) and the
	// import is running, or waiting its turn. Progress, when it's copying.
	Importing bool
	Progress  string
}

// Failure is an import that has failed and will be retried.
type Failure struct {
	Attempts  int
	NextRetry time.Time // zero: on the next import check
	LastErr   string
}

// Input is everything known about one torrent.
type Input struct {
	Phase    string // download.Item.Phase(): queued, metadata, downloading, stalled, checking, moving, allocating, paused, seeding, error, unpacking
	Complete bool   // every wanted byte is on disk
	// Stalled is the queue's stall verdict (automation.QueueStall): trying to download and
	// no progress for the fail-over's grace. The client's own "stalled" phase alone isn't
	// one — qBittorrent says it whenever no peer has sent anything for a few seconds.
	Stalled bool
	// Usenet: a usenet job, which never seeds — finished and imported, it is Imported.
	Usenet bool
	// FailMessage is a failed usenet job's reason as its client gave it, and FailFix what
	// to do when the failure is where the client writes rather than the release (a full
	// disk): Arrmada retries nothing then, a person has to.
	FailMessage string
	FailFix     string
	// Linked: an Arrmada grab recorded this torrent's hash (the acquisition record).
	Linked bool
	// GrabStatus is that grab's status word (grabbed, held, imported, dismissed).
	GrabStatus string
	// Imported: the imports table has the hash — it's in the library (or was settled).
	Imported bool
	// OwnCategory: it sits in one of Arrmada's download categories, so the import sweep
	// considers it even without a grab.
	OwnCategory bool
	Review      *Review
	Failure     *Failure
	HeldByGuard bool
	// WrongCategory: the series sweep found it finished in a category it doesn't import
	// from, matching a show in the library. Category is the one it's in.
	WrongCategory bool
	Category      string
	Now           time.Time
}

// Describe is the stage of one torrent and a short reason for staff ("Import failed 3×
// (permission denied) — next try in 8 min"). The reason is admin detail: never show it
// to a requester.
func Describe(in Input) (stage, why string) {
	if !in.Complete {
		switch {
		case in.Phase == Unpacking:
			return Unpacking, "SABnzbd is checking and unpacking it"
		case in.HeldByGuard && in.Phase == "paused":
			return PausedByGuard, "Paused by the disk guard — it resumes once the downloads volume has room"
		case in.Phase == "error" && in.Usenet:
			return Failed, usenetFailWhy(in)
		case in.Phase == "error":
			return Failed, "The download client stopped it with an error"
		case in.Phase == "paused":
			return Paused, ""
		case in.Phase == "queued" && in.Usenet:
			return Queued, "Waiting its turn in SABnzbd"
		case in.Phase == "queued":
			return Queued, "Waiting for a download slot"
		case in.Stalled && in.Phase == "metadata":
			return Stalled, "Nobody has sent the file list"
		case in.Stalled:
			return Stalled, "Nobody is sending data"
		case in.Phase == "metadata":
			return FetchingMetadata, "Waiting for peers to send the file list"
		case in.Phase == "stalled":
			// No peer is sending this moment, but not for long enough to call it stalled.
			return Downloading, "Waiting for peers"
		}
		return Downloading, ""
	}

	imported := in.Imported || in.GrabStatus == "imported" || in.GrabStatus == "dismissed"
	if imported {
		if in.Phase == "paused" || in.Usenet {
			// A usenet job never seeds: it's in the library, and clean-up archives the job.
			return Imported, ""
		}
		return Seeding, ""
	}
	switch {
	case in.Review != nil && in.Review.Importing:
		// Still pending until the import lands, but nobody needs to decide anything now.
		why := "Importing — chosen in Needs you"
		if in.Review.Progress != "" {
			why += ": " + in.Review.Progress
		}
		return Importing, why
	case in.Review != nil || in.GrabStatus == "held":
		return NeedsReview, reviewWhy(in.Review)
	case in.Failure != nil:
		return ImportRetrying, failureWhy(*in.Failure, in.Now)
	case in.WrongCategory:
		cat := "no category"
		if in.Category != "" {
			cat = "category “" + in.Category + "”"
		}
		return WrongCategory, "In " + cat + " — it won't import as TV"
	case in.Linked:
		return Importing, "Waiting for the next import check"
	case in.OwnCategory:
		return Downloaded, "Finished — it imports if it matches a title in the library"
	}
	return NotManaged, "Not managed by Arrmada"
}

// usenetFailWhy is a failed usenet job's reason: the client's own words, and what to do
// when it's the client's disk or folders at fault rather than the release.
func usenetFailWhy(in Input) string {
	why := "SABnzbd failed it"
	if m := strings.TrimSpace(in.FailMessage); m != "" {
		if r := []rune(m); len(r) > 160 {
			m = string(r[:157]) + "…"
		}
		why += ": " + m
	}
	if in.FailFix != "" {
		return why + " — " + in.FailFix
	}
	if in.Linked && in.GrabStatus == "grabbed" {
		return why + " — Arrmada blocklists it and tries another release"
	}
	return why
}

func reviewWhy(r *Review) string {
	if r == nil {
		return "Held for review"
	}
	switch r.Code {
	case reviewMismatch:
		if r.ExpectedTitle != "" {
			return "Held for review: it doesn't look like " + r.ExpectedTitle
		}
		return "Held for review: it looks like a different title"
	case reviewUnmatched:
		return "Held for review: it matches nothing in the library"
	case reviewNumbering:
		return "Held for review: its episode numbers couldn't be read"
	case reviewImportFailed:
		return "Held for review: the import keeps failing"
	case reviewNoMedia:
		return "Held for review: nothing inside can be imported"
	}
	return "Held for review"
}

func failureWhy(f Failure, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Import failed %d×", max(f.Attempts, 1))
	if e := strings.TrimSpace(f.LastErr); e != "" {
		if r := []rune(e); len(r) > 160 {
			e = string(r[:157]) + "…"
		}
		b.WriteString(" (" + e + ")")
	}
	if wait := f.NextRetry.Sub(now); !f.NextRetry.IsZero() && wait > 0 {
		b.WriteString(" — next try in " + roughWait(wait))
	} else {
		b.WriteString(" — tried again on the next import check")
	}
	return b.String()
}

// roughWait is a wait in whole minutes, or hours past two.
func roughWait(d time.Duration) string {
	m := int((d + time.Minute - 1) / time.Minute)
	if m < 120 {
		return fmt.Sprintf("%d min", max(m, 1))
	}
	return fmt.Sprintf("%d h", (m+30)/60)
}
