package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

// Hand imports: the imports a person starts from Activity → Needs you — a held download's
// Import anyway, Choose different… or Map files…, and Import into… for a download Arrmada
// didn't grab. Each can be a long copy onto another drive, so each runs as a job the page
// follows, with the copy's progress, one at a time per download.

// reviewImportKind is the one job kind for every import out of a review — Import anyway,
// Choose different…, Map files… — so the runner's single-flight keeps a review to one
// import at a time.
const reviewImportKind = "review.import"

// downloadImportKind is the job that imports a download Arrmada didn't grab, one per
// torrent.
const downloadImportKind = "download.import"

// handImportRequest is one hand import asked for.
type handImportRequest struct {
	Kind, Target string // the job's (kind, target): one import per download
	Hash         string // the download's, so the queue can show it importing
	// Key is what was asked for (the target item, a mapping): the same key again is the
	// same import, and joins it; a different one while it runs is refused.
	Key   string
	Title string // what it goes into, for "Imported into …"
	Fn    func(context.Context, *jobs.Progress) (any, error)
}

// handImports remembers what each running hand import was asked to do, by job target, so
// a different request for the same download can be told what it would have joined. The
// zero value is ready to use.
type handImports struct {
	mu      sync.Mutex
	running map[string]runningHandImport
}

type runningHandImport struct {
	job        int64
	key, title string
}

// startHandImport runs req as a job and answers 202 {job_id, existing, title, …fields}.
// When the download is already importing, the same request joins that import (with its
// title), and a different one — another title, another mapping — is refused with 409,
// naming the running job and what it's importing into: silently joining it would report
// the other import's outcome as this one's.
func (a *api) startHandImport(w http.ResponseWriter, r *http.Request, req handImportRequest, fields map[string]any) {
	hi := &a.handImports
	var jobID int64 // set under hi.mu before Finished can read it (it takes hi.mu too)
	spec := jobs.Spec{Kind: req.Kind, Target: req.Target, Class: jobs.ClassImport,
		// A copy onto another drive runs at disk speed — a season pack or a remux can
		// take an hour — so it gets a generous budget, in the import lane.
		Timeout: 12 * time.Hour,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			p.Set(0, "Importing…")
			if req.Hash != "" {
				a.queueChanged(req.Hash) // the queue shows it Importing at once
			}
			return req.Fn(ctx, p)
		},
		// Once the job has left the runner's Active list, so the queue re-reads it as over.
		Finished: func(string) {
			hi.mu.Lock()
			if cur, ok := hi.running[req.Target]; ok && cur.job == jobID {
				delete(hi.running, req.Target)
			}
			hi.mu.Unlock()
			if req.Hash != "" {
				a.queueChanged(req.Hash)
			}
		}}

	hi.mu.Lock()
	id, existing, err := a.submit(r, spec)
	if err != nil {
		hi.mu.Unlock()
		a.writeError(w, http.StatusServiceUnavailable, "couldn't start that just now — try again in a moment")
		return
	}
	cur, known := hi.running[req.Target]
	if !existing && id > 0 {
		jobID = id
		if hi.running == nil {
			hi.running = map[string]runningHandImport{}
		}
		hi.running[req.Target] = runningHandImport{job: id, key: req.Key, title: req.Title}
	}
	hi.mu.Unlock()

	title := req.Title
	if existing && known && cur.job == id {
		if cur.key != req.Key {
			msg := "This download is already being imported"
			if cur.title != "" {
				msg += " into " + cur.title
			}
			a.writeJSON(w, http.StatusConflict, map[string]any{"status": "error", "message": msg + " — wait for that to finish.",
				"existing": true, "job_id": id, "title": cur.title})
			return
		}
		title = cur.title
	}
	body := map[string]any{"status": "importing", "title": title}
	for k, v := range fields {
		body[k] = v
	}
	a.accepted(w, id, existing, body)
}

// withImportProgress makes a hand import report its copying as the job's progress:
// "Copying… 12.3 of 48.0 GB". A hardlinked import is instant and stays at "Importing…".
func withImportProgress(ctx context.Context, p *jobs.Progress) context.Context {
	return automation.WithImportProgress(ctx, func(copied, total int64) {
		p.Set(copyProgress(copied, total))
	})
}

// copyProgress is a hand import's copy as a job's progress and message.
func copyProgress(copied, total int64) (float64, string) {
	pct := 0.0
	if total > 0 {
		// Short of full: placing and checking the files comes after the last byte.
		pct = float64(copied) / float64(total) * 0.95
	}
	return pct, "Copying… " + sizeGB(copied, false) + " of " + sizeGB(total, true)
}

// sizeGB is a byte count in gigabytes, one decimal: "12.3", or "48.0 GB" with the unit.
func sizeGB(n int64, unit bool) string {
	s := strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64)
	if unit {
		s += " GB"
	}
	return s
}

// importedInto is a finished hand import's message and result.
func importedInto(p *jobs.Progress, title string) map[string]any {
	msg := "Imported."
	if title != "" {
		msg = "Imported into " + title + "."
	}
	p.SetMessage(msg)
	return map[string]any{"imported": true, "title": title}
}

// activeHandImports is the hand imports running or queued right now: review imports by
// review id, Import into… by lowercased hash. It reads the runner's memory — the one
// record that can't be left saying "running" by a lost write — and only a runner without
// one (a test's) is asked through the jobs table.
func (a *api) activeHandImports(ctx context.Context) (byReview map[int64]jobs.Job, byHash map[string]jobs.Job) {
	act, ok := a.deps.Jobs.(interface{ Active() []jobs.Job })
	if !ok {
		byReview, byHash = a.reviewImportJobs(ctx), a.downloadImportJobs(ctx)
		for id, j := range byReview {
			if jobs.Terminal(j.Status) {
				delete(byReview, id)
			}
		}
		for h, j := range byHash {
			if jobs.Terminal(j.Status) {
				delete(byHash, h)
			}
		}
		return byReview, byHash
	}
	byReview, byHash = map[int64]jobs.Job{}, map[string]jobs.Job{}
	for _, j := range act.Active() {
		if n, ok := reviewOfJob(j); ok {
			byReview[n] = j
		} else if h, ok := hashOfJob(j); ok {
			byHash[h] = j
		}
	}
	return byReview, byHash
}

// reviewImportJobs is the newest import job of each review that has one, by review id,
// from the jobs table: how the last one ended.
func (a *api) reviewImportJobs(ctx context.Context) map[int64]jobs.Job {
	out := map[int64]jobs.Job{}
	if a.deps.Jobs == nil {
		return out
	}
	list, err := a.deps.Jobs.List(ctx, jobs.Filter{Kind: reviewImportKind, Limit: 500})
	if err != nil {
		return out
	}
	for _, j := range list { // newest first
		if n, ok := reviewOfJob(j); ok {
			if _, seen := out[n]; !seen {
				out[n] = j
			}
		}
	}
	return out
}

// downloadImportJobs is the newest Import into… job of each torrent that has one, by
// lowercased hash, from the jobs table.
func (a *api) downloadImportJobs(ctx context.Context) map[string]jobs.Job {
	out := map[string]jobs.Job{}
	if a.deps.Jobs == nil {
		return out
	}
	list, err := a.deps.Jobs.List(ctx, jobs.Filter{Kind: downloadImportKind, Limit: 500})
	if err != nil {
		return out
	}
	for _, j := range list { // newest first
		if h, ok := hashOfJob(j); ok {
			if _, seen := out[h]; !seen {
				out[h] = j
			}
		}
	}
	return out
}

// shownImportJob is the import job a card shows: the one running now, else the last one
// when it failed (so a reloaded page says why). A job the table still calls running but
// the runner doesn't hold is over, whatever the row says.
func shownImportJob(active jobs.Job, running bool, last jobs.Job, hasLast bool) *jobs.Job {
	if running {
		return &active
	}
	if hasLast && reviewJobFailed(last.Status) {
		return &last
	}
	return nil
}

// reviewJobFailed reports whether a finished job ended badly enough to show on its card.
func reviewJobFailed(status string) bool {
	switch status {
	case jobs.StatusFailed, jobs.StatusPanicked, jobs.StatusInterrupted:
		return true
	}
	return false
}

// reviewOfJob is the review a review import job is for.
func reviewOfJob(j jobs.Job) (int64, bool) {
	if j.Kind != reviewImportKind {
		return 0, false
	}
	s, ok := strings.CutPrefix(j.Target, "review:")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// hashOfJob is the torrent an Import into… job is for.
func hashOfJob(j jobs.Job) (string, bool) {
	if j.Kind != downloadImportKind {
		return "", false
	}
	h, ok := strings.CutPrefix(j.Target, "torrent:")
	return h, ok && h != ""
}
