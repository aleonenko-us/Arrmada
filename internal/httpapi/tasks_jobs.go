package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/scheduler"
)

// Recent jobs, under the Tasks table on System → Status: the work people and events
// started (a Search click, an approval's search, a scan, a Run now), in words, each linked
// to its movie, show, book or album, with who started it, how it went and how long it took.
// /api/v1/jobs serves the raw rows buttons follow; this is the same rows made readable.

// recentJob is one row of the Recent jobs list.
type recentJob struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"` // what the work was, in words
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	// What it was for: a name, and the in-app page it opens ("" when there's none).
	Target      string `json:"target"`
	TargetLabel string `json:"target_label,omitempty"`
	TargetPath  string `json:"target_path,omitempty"`
	// Who or what started it.
	TriggerLabel string     `json:"trigger_label"`
	TriggerPath  string     `json:"trigger_path,omitempty"`
	CreatedAt    *time.Time `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	// DurationMS is how long it ran (so far, while it's running); 0 before it starts.
	DurationMS int64 `json:"duration_ms"`
}

// jobKindLabels names the kinds of work in words. A kind missing here is spelled out
// from its key ("movie.media-backfill" → "Movie media backfill").
var jobKindLabels = map[string]string{
	"movie.search":              "Search for a movie",
	"movie.upgrade":             "Look for a better release",
	"movie.regrab":              "Replace a movie download",
	"movie.scan":                "Scan the movies folder",
	"movie.organize":            "Organise movie files",
	"movie.media-backfill":      "Read movie file details",
	"series.search":             "Search for a show",
	"series.grab-scope":         "Grab episodes",
	"series.regrab-episode":     "Replace an episode",
	"series.scan":               "Scan the TV folder",
	"series.refresh-all":        "Refresh every show",
	"series.import-folder":      "Import a show's folder",
	"series.numbering-apply":    "Apply new episode numbering",
	"book.search":               "Search for a book",
	"book.search-version":       "Search for a book edition",
	"book.merge-audiobook":      "Merge audiobook editions",
	"books.scan":                "Scan the books folder",
	"books.backfill-series":     "Fill in book series",
	"books.add-author-search":   "Search an author's books",
	"books.refresh-new":         "Look for new books in a series",
	"books.search-missing":      "Search for missing books",
	"books.upgrade":             "Look for better book releases",
	"album.search":              "Search for an album",
	"music.scan":                "Scan the music folder",
	"download.block":            "Block a download and search again",
	"download.failover":         "Replace a stalled download",
	"review.import":             "Import a reviewed download",
	"download.import":           "Import a download Arrmada didn't grab",
	"review.map":                "Import a reviewed download", // older rows: mapping imports are review.import now
	"indexer.caps":              "Read Prowlarr's indexer capabilities",
	"health.check":              "Run health checks",
	"library.apply-folders":     "Apply new library folders",
	"convert.reindex":           "Re-read the Convert library",
	"subtitles.sync-check":      "Check subtitle timing",
	"requests.import-overseerr": "Import from Overseerr",
	"insights.import-tautulli":  "Import from Tautulli",
	"insights.remove-import":    "Remove a Tautulli import",
	"insights.remove-overlaps":  "Remove overlapping plays",
	"audioserver.warm":          "Prepare the audiobook server",
	"kindle.send":               "Send to Kindle",
	scheduler.JobKind:           "Run now",
}

// jobKindLabel is a kind in words.
func jobKindLabel(kind string) string {
	if l, ok := jobKindLabels[kind]; ok {
		return l
	}
	s := strings.NewReplacer(".", " ", "-", " ", "_", " ").Replace(kind)
	if s == "" {
		return "Background work"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

const (
	recentJobsDefault = 50
	recentJobsMax     = 200
)

// handleRecentJobs is GET /api/v1/system/jobs?limit=50: the newest jobs, readable.
func (a *api) handleRecentJobs(w http.ResponseWriter, r *http.Request) {
	limit := recentJobsDefault
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			a.writeError(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		limit = min(n, recentJobsMax)
	}
	out := []recentJob{}
	if a.deps.Jobs == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
		return
	}
	list, err := a.deps.Jobs.List(r.Context(), jobs.Filter{Limit: limit})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list jobs")
		return
	}
	names := newJobNamer(a)
	now := time.Now()
	for _, j := range list {
		row := recentJob{
			ID: j.ID, Kind: j.Kind, Label: jobKindLabel(j.Kind), Status: j.Status, Message: j.Message, Error: j.Error,
			Target: j.Target, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
		}
		row.TargetLabel, row.TargetPath = names.target(r.Context(), j.Kind, j.Target)
		row.TriggerLabel, row.TriggerPath = names.trigger(r.Context(), j.Trigger)
		if j.StartedAt != nil {
			end := now
			if j.FinishedAt != nil {
				end = *j.FinishedAt
			}
			row.DurationMS = max(end.Sub(*j.StartedAt).Milliseconds(), 0)
		}
		out = append(out, row)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// jobNamer looks up the names a page of jobs points at, each once.
type jobNamer struct {
	a     *api
	cache map[string][2]string
}

func newJobNamer(a *api) *jobNamer { return &jobNamer{a: a, cache: map[string][2]string{}} }

// target names a job's target and links it. A kind's own target ("all", a task name) is
// read with the kind; "movie:12", "series:3:s1e2", "book:5:v2", "album:9" open their
// pages. A target that names nothing readable (a Kindle send's reference, a torrent hash)
// is shown as no target rather than as raw text.
func (n *jobNamer) target(ctx context.Context, kind, target string) (string, string) {
	if kind == scheduler.JobKind {
		label := target
		if n.a.deps.Scheduler != nil {
			if st, ok := n.a.deps.Scheduler.Status(target); ok && st.Label != "" {
				label = st.Label
			}
		}
		return label, "/settings/status#tasks"
	}
	if target == "" || target == "all" {
		return "", ""
	}
	if hit, ok := n.cache[target]; ok {
		return hit[0], hit[1]
	}
	label, path := n.lookup(ctx, target)
	n.cache[target] = [2]string{label, path}
	return label, path
}

func (n *jobNamer) lookup(ctx context.Context, target string) (string, string) {
	parts := strings.SplitN(target, ":", 3)
	if len(parts) < 2 {
		return "", ""
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	switch parts[0] {
	case "hash", "torrent":
		return "A download", "/activity/queue"
	case "review":
		return "A held download", "/activity/needs-you"
	}
	if err != nil || id <= 0 {
		return "", ""
	}
	d := n.a.deps
	switch parts[0] {
	case "movie":
		path := "/movies/" + parts[1]
		if d.Movies != nil {
			if m, err := d.Movies.Get(ctx, id); err == nil {
				return withYear(m.Title, m.Year), path
			}
		}
		return "Movie #" + parts[1], path
	case "series":
		path := "/series/" + parts[1]
		if d.Series != nil {
			if s, err := d.Series.Get(ctx, id); err == nil {
				return s.Title, path
			}
		}
		return "Show #" + parts[1], path
	case "book":
		path := "/books/" + parts[1]
		if d.Books != nil {
			if b, err := d.Books.Get(ctx, id); err == nil {
				return b.Title, path
			}
		}
		return "Book #" + parts[1], path
	case "album":
		path := "/music/album/" + parts[1]
		if d.Music != nil {
			if al, err := d.Music.GetAlbum(ctx, id); err == nil {
				return al.Title, path
			}
		}
		return "Album #" + parts[1], path
	}
	return "", ""
}

// trigger says who or what started a job: a person by name, an approved request, or
// Arrmada itself.
func (n *jobNamer) trigger(ctx context.Context, trigger string) (string, string) {
	kind, rest, _ := strings.Cut(trigger, ":")
	switch kind {
	case "user":
		id, err := strconv.ParseInt(rest, 10, 64)
		if err == nil && n.a.deps.Auth != nil {
			key := "user:" + rest
			if hit, ok := n.cache[key]; ok {
				return hit[0], ""
			}
			name := "Someone"
			if u, err := n.a.deps.Auth.UserByID(ctx, id); err == nil && u != nil {
				name = u.Username
			}
			n.cache[key] = [2]string{name, ""}
			return name, ""
		}
		return "Someone", ""
	case "request":
		if _, err := strconv.ParseInt(rest, 10, 64); err == nil {
			return "Request #" + rest, "/requests?id=" + rest
		}
		return "A request", ""
	case "api":
		return "The API", ""
	}
	return "Arrmada", ""
}

// withYear is "Title (Year)", or the title alone when the year isn't known.
func withYear(title string, year int) string {
	if title != "" && year > 0 {
		return title + " (" + strconv.Itoa(year) + ")"
	}
	return title
}
