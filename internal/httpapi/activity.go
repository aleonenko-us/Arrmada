package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/pipeline"
)

// handleDownloadsFeed returns the live download queue, each torrent with the library
// title it was grabbed for (poster, scope, requesters) and its resolved quality profile.
// (Served at /downloads, not /activity — the latter is blocked by common ad-blocker filter
// lists.) The titles still being searched for are the Wanted view's (GET /api/v1/wanted):
// it reads the search history too, which a queue read shouldn't.
func (a *api) handleDownloadsFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// One shared read of the clients. When it failed or missed a client, what's
	// downloading isn't known, and the page must say so rather than show an empty queue.
	snap, _, qerr := a.queueSnapshot(ctx)
	queue := snap.Items

	// The acquisition record, joined to the queue by info hash: which library item each
	// torrent was grabbed for and under which profile, whatever the torrent is called,
	// and the seed goal recorded at grab time (so the Seeding tab can show each torrent's
	// target ratio / time and whether it's set to seed at all). Matching torrent names
	// against titles mislabelled prettified names and called Arrmada's own downloads "Not
	// managed by Arrmada". Cached in the coordinator until a grab is written, so a
	// refresh on every queue event doesn't re-read the record.
	var links automation.QueueLinks
	// How each in-flight grab stands against its stall window, so the page can say how
	// long a download has made no progress and when another release will be tried.
	var stallInfo map[string]automation.StallState
	if a.deps.Automation != nil {
		links, _ = a.deps.Automation.QueueLinks(ctx)
		stallInfo = a.deps.Automation.StallInfo(ctx)
	}
	live, legacy, seedPolicies := links.ByHash, links.Legacy, links.Seed
	// Resolve each quality-profile reference to a name at most once per request, and
	// across requests for a couple of minutes.
	profileCache := map[string]string{}
	pname := func(ref string) string {
		if v, ok := profileCache[ref]; ok {
			return v
		}
		v := a.cachedProfileName(ctx, ref, time.Now())
		profileCache[ref] = v
		return v
	}

	// Imported hashes are flagged (not hidden) so the Seeding tab can show every seeding
	// torrent — the user wants to see each one's seed rule and progress, not just the
	// untracked ones. Only the queue's own hashes are looked up, not the whole history.
	imported := map[string]bool{}
	if a.deps.Library != nil && len(queue) > 0 {
		hashes := make([]string, 0, len(queue))
		for _, it := range queue {
			hashes = append(hashes, it.Hash)
		}
		if s, err := a.deps.Library.ImportedAmong(ctx, hashes); err == nil {
			imported = s
		}
	}

	// What the disk guard is holding, so a guard pause reads differently from one the
	// user made — and the page doesn't offer a Resume that the server will refuse.
	guardHeld, guardSt := a.guardHolding(ctx)
	heldCount := 0

	// Where each finished torrent stands on its way into the library — held for review,
	// an import being retried, filed where nothing imports it — so the page can say why
	// it's still there instead of showing it as a healthy seed.
	fin := a.finishingFacts(ctx)
	now := time.Now()
	// Who is waiting on each title, for the cards' "for Mum" chips; read only when
	// something in the queue was grabbed for a title.
	var reqs map[string][]string
	if len(live) > 0 || len(legacy) > 0 {
		reqs = a.requestersByLibrary(ctx, now)
	}

	// The download clients, read once: whether there's one at all and whether it answered
	// (the "clients" block below), and each one's name for the rows, so someone with
	// several can tell which client a download is in. Left out when the list can't be read.
	clients, clientsErr := a.deps.Downloads.List(ctx)
	clientName := make(map[int64]string, len(clients))
	for _, c := range clients {
		clientName[c.ID] = c.Name
	}

	downloads := make([]map[string]any, 0, len(queue))
	var totalDown, totalUp int64
	active, stalled := 0, 0
	for _, it := range queue {
		// By hash; a legacy grab recorded without one is the only thing still paired by
		// name. A torrent no grab knows keeps its category's media type and no profile.
		acq, managed := live[strings.ToLower(it.Hash)]
		if !managed {
			acq, managed = automation.LegacyMatchByName(legacy, it)
		}
		profile := "n/a"
		mediaType := queueMediaType(it.Category)
		if managed {
			mediaType = acq.MediaType
			if acq.Profile != "" {
				profile = pname(acq.Profile)
			}
		}
		totalDown += it.DownSpeed
		totalUp += it.UpSpeed
		if it.State == "downloading" {
			active++
		}
		phase := it.Phase()
		h := strings.ToLower(it.Hash)
		_, isHeld := guardHeld[h]
		// Stalled is the one rule this page and a requester's card share, built on the stall
		// fail-over's own clock (automation.QueueStall): an Arrmada grab by the clock the
		// fail-over counts, anything else by the client's own stalled state — each held for
		// the fail-over's grace. qBittorrent's "stalledDL" alone flickers on and off.
		var clock time.Time
		if managed {
			clock = acq.ProgressAt
		}
		stallSince, isStalled := automation.QueueStall(it, clock, isHeld, now)
		if isStalled {
			stalled++
		}
		entry := map[string]any{
			"hash":        it.Hash,
			"name":        it.Name,
			"state":       it.State,
			"progress":    it.Progress,
			"size_bytes":  it.SizeBytes,
			"down_speed":  it.DownSpeed,
			"up_speed":    it.UpSpeed,
			"eta_seconds": it.ETASeconds,
			// The client's own state and the finer phase read from it, with the swarm
			// numbers: "downloading" alone can't tell a dead torrent from a live one.
			"raw_state":     it.RawState,
			"phase":         phase,
			"seeds":         it.Seeds,
			"peers":         it.Peers,
			"swarm_seeds":   it.SwarmSeeds,
			"last_activity": it.LastActivity,
			"added_on":      it.AddedOn,
			// The computed ratio, not the client's field: qBittorrent reports an unbounded
			// ratio as a 9999 sentinel, so the page would show that while the seed-goal
			// logic (which now works from the byte counters) sees the real figure.
			"ratio":           ratioOrZero(it.SeedRatio()),
			"uploaded_bytes":  it.UploadedBytes,
			"seeding_time":    it.SeedingTime,
			"quality_profile": profile,
			"media_type":      mediaType,
			"imported":        imported[it.Hash],
		}
		// The title it was grabbed for: poster, name and link, what part of it the torrent
		// covers and who asked for it. By the grab alone — a torrent no grab knows keeps
		// its release name, however much its name looks like a library title.
		if managed {
			entry["managed"] = true
			if item := a.transferItemFor(ctx, acq, reqs, now); item != nil {
				entry["item"] = item
			}
		}
		if st, ok := stallInfo[h]; ok && !it.Complete() {
			entry["stall"] = st
		}
		if isStalled {
			entry["stalled"] = true
			entry["stalled_since"] = stallSince.Unix()
		}
		// Which download client it's in, by the name its card has in Settings → Connections.
		// The page shows it only when there's more than one client.
		if it.ClientID > 0 {
			entry["client_id"] = it.ClientID
			if name := clientName[it.ClientID]; name != "" {
				entry["client_name"] = name
			}
		}
		if it.State == "paused" && isHeld {
			entry["held_by_guard"] = true
			heldCount++
		}
		// One stage model for staff and requesters (internal/pipeline): the stage decides
		// which part of the queue a torrent sits in, and why says what's holding it.
		in := pipeline.Input{
			Phase: phase, Complete: it.Complete(), Linked: managed, Imported: imported[it.Hash] || imported[h],
			OwnCategory: fin.own[it.Category], HeldByGuard: isHeld, Category: it.Category, Now: now,
			Usenet: it.Transport == download.TransportUsenet, Stalled: isStalled,
		}
		// The network, so the page can mark a usenet job and leave out what only a torrent
		// has (seeds, ratio, reannounce). A failed usenet job's reason, and whether it's the
		// release (Arrmada replaces it) or where SABnzbd writes (a person must fix it).
		entry["transport"] = it.Transport
		entry["remaining_bytes"] = it.RemainingBytes
		if in.Usenet && phase == "error" {
			f := automation.ClassifyUsenetFailure(it.FailMessage)
			entry["fail_message"], entry["fail_kind"] = it.FailMessage, f.Kind
			in.FailMessage, in.FailFix = it.FailMessage, f.Fix
		}
		if managed {
			in.GrabStatus = acq.Status
		}
		if rv, ok := fin.reviews[h]; ok {
			in.Review = &rv
		}
		if f, ok := fin.failures[h]; ok {
			in.Failure = &f
		}
		in.WrongCategory = fin.wrongCat[h]
		stage, why := pipeline.Describe(in)
		entry["stage"] = stage
		if why != "" {
			entry["why"] = why
		}
		// By info hash: the indexer's listing title is often a prettified rendering of the
		// actual torrent, so matching on the name missed entire trackers and labelled
		// genuinely-managed torrents "Not managed by Arrmada". Only a legacy grab without a
		// hash is still keyed by its release name.
		p, ok := seedPolicies[strings.ToLower(it.Hash)]
		if !ok {
			p, ok = seedPolicies[automation.NormReleaseKey(it.Name)]
		}
		if ok {
			entry["seed_enabled"] = p.Enabled
			entry["seed_ratio"] = p.Ratio
			entry["seed_hours"] = p.Hours
			entry["seed_known"] = true
		}
		downloads = append(downloads, entry)
	}

	out := map[string]any{
		"downloads": downloads,
		"totals":    map[string]any{"down_speed": totalDown, "up_speed": totalUp, "active": active, "stalled": stalled},
	}
	// Only a real reading: a folder that can't be measured used to show as "free 0 GB",
	// which reads as a full disk. null says "no figure".
	dlDir := a.roots().Downloads(ctx)
	out["free_gb"], out["disk_path"] = nil, dlDir
	if freeGB, ok := freeGBField(dlDir); ok {
		out["free_gb"] = freeGB
	}
	// Whether there is a download client at all and whether it answered, so the page can
	// say "no download client yet" or "qBittorrent unreachable since 14:02" instead of
	// looking like an empty queue. Left out when the list can't be read rather than
	// guessing.
	if clientsErr == nil {
		out["clients"] = clientsStateOf(clients, snap, qerr)
	}
	if heldCount > 0 {
		out["disk_guard"] = map[string]any{
			"holding": heldCount, "used_pct": guardSt.UsedPct,
			"pause_pct": guardSt.PausePct, "resume_pct": guardSt.ResumePct,
		}
	}
	a.writeJSON(w, http.StatusOK, out)
}

// finishing is what the feed needs to tell where a finished torrent stands, each keyed by
// lowercased hash: pending reviews, imports being retried, misfiled TV downloads, and
// Arrmada's own categories (which the import sweeps read without a grab).
type finishing struct {
	reviews  map[string]pipeline.Review
	failures map[string]pipeline.Failure
	wrongCat map[string]bool
	own      map[string]bool
}

func (a *api) finishingFacts(ctx context.Context) finishing {
	f := finishing{reviews: map[string]pipeline.Review{}, failures: map[string]pipeline.Failure{}, wrongCat: map[string]bool{}, own: map[string]bool{}}
	cats := download.FixedCategories(a.deps.Config.DownloadCategory)
	for _, c := range []string{cats.Movies, cats.TV, cats.Books, cats.Music} {
		f.own[c] = true
	}
	for _, fl := range a.importFailures() {
		if fl.Hash != "" {
			f.failures[strings.ToLower(fl.Hash)] = pipeline.Failure{Attempts: fl.Attempts, NextRetry: fl.NextRetry, LastErr: fl.LastErr}
		}
	}
	if a.deps.Automation == nil {
		return f
	}
	// A download someone chose to import from Needs you (a held one, or one Arrmada didn't
	// grab) is importing, not waiting on anyone: its job is running or queued.
	importing, importingHash := a.activeHandImports(ctx)
	handImport := func(pr *pipeline.Review, j jobs.Job) {
		pr.Importing = true
		if strings.HasPrefix(j.Message, "Copying") {
			pr.Progress = j.Message
		}
	}
	// Unreadable reviews leave a held grab as "held for review" without the reason. Cached
	// until a review is held or settled.
	if rvs, err := a.deps.Automation.PendingReviews(ctx); err == nil {
		for _, rv := range rvs {
			if rv.Hash != "" {
				pr := pipeline.Review{Code: rv.ReasonCode, ExpectedTitle: rv.ExpectedTitle}
				if j, ok := importing[rv.ID]; ok {
					handImport(&pr, j)
				}
				f.reviews[strings.ToLower(rv.Hash)] = pr
			}
		}
	}
	for h, j := range importingHash {
		if _, held := f.reviews[h]; !held {
			var pr pipeline.Review
			handImport(&pr, j)
			f.reviews[h] = pr
		}
	}
	for _, it := range a.deps.Automation.WrongCategoryDownloads() {
		f.wrongCat[strings.ToLower(it.Hash)] = true
	}
	return f
}

// importFailures is every download whose import has failed and will be retried: the
// movie importer's and the series sweep's (Deps.ImportFailures), else the importer's own.
func (a *api) importFailures() []library.FailureInfo {
	if a.deps.ImportFailures != nil {
		return a.deps.ImportFailures()
	}
	if a.deps.Library != nil {
		return a.deps.Library.Failures()
	}
	return nil
}

// queueMediaType is what a queued torrent is by its download category — each kind is
// grabbed into its own (download.Categories). It labels a torrent no grab knows; one
// Arrmada grabbed takes its media type and profile from the acquisition record.
func queueMediaType(category string) string {
	switch category {
	case download.CategoryTV:
		return "series"
	case download.CategoryBooks:
		return "book"
	case download.CategoryMusic:
		return "music"
	}
	return "movie"
}

// profileName resolves a profile reference to a friendly name.
func (a *api) profileName(ctx context.Context, ref string) string {
	if sp, err := a.deps.Quality.GetStored(ctx, ref); err == nil && sp.Name != "" {
		return sp.Name
	}
	return ref
}

// movieDownload returns the in-progress download for a movie that doesn't yet have a file,
// through its acquisitions: the torrent each was grabbed as, found by info hash, so a
// torrent named nothing like the film still shows its progress. Only actively-downloading
// items (progress < 100%) are reported — a completed torrent is left to the import
// pipeline, so the UI never shows a stuck "importing 100%" for a seed that isn't really
// being imported.
func movieDownload(m movies.Movie, acqs []automation.Acquisition, byHash map[string]download.Item, queue []download.Item) *movies.DownloadStatus {
	if m.HasFile {
		return nil
	}
	if it, ok := inProgress(acqs, byHash, queue); ok {
		return &movies.DownloadStatus{State: it.State, Progress: it.Progress, Hash: it.Hash}
	}
	return nil
}

// inProgress is the first of acqs whose torrent is in the queue and not finished.
func inProgress(acqs []automation.Acquisition, byHash map[string]download.Item, queue []download.Item) (download.Item, bool) {
	for _, a := range acqs {
		if it, ok := automation.QueueItemFor(a, byHash, queue); ok && it.Progress < 1 {
			return it, true
		}
	}
	return download.Item{}, false
}

// queueByHash indexes a queue read by lowercased info hash.
func queueByHash(queue []download.Item) map[string]download.Item {
	out := make(map[string]download.Item, len(queue))
	for _, it := range queue {
		out[strings.ToLower(it.Hash)] = it
	}
	return out
}

// ratioOrZero renders an unknowable ratio as 0 rather than the -1 sentinel, which would
// show up in the UI as a negative ratio.
func ratioOrZero(r float64) float64 {
	if r < 0 {
		return 0
	}
	return r
}

// freeGBField is the downloads feed's free-space figure and whether there is one to show.
func freeGBField(path string) (float64, bool) {
	if path == "" {
		return 0, false
	}
	return diskspace.FreeGB(path)
}
