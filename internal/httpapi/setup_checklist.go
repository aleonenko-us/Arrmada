package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/health"
)

// Getting started: the steps between a fresh install and the first imported download, each
// ticked off by looking at what's actually configured rather than by anyone clicking "done".
// The Dashboard shows it as a card until everything is ticked or the admin dismisses it, and
// the setup wizard's last step shows the same list.

const keyGettingStartedDismissed = "getting_started_dismissed"

// checklistProbeTimeout bounds the one check that talks to something else (the download
// client), so a hung client can't hold the Dashboard card open.
const checklistProbeTimeout = 3 * time.Second

// checklistItem is one step. Link and LinkKey say where to do it, the same way a health
// warning's Fix does: LinkKey names the web UI's LINKS entry, Link is the path for anyone else.
type checklistItem struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Done    bool   `json:"done"`
	Link    string `json:"link"`
	LinkKey string `json:"link_key,omitempty"`
	// Reason says, in plain words, why a configured step's connection isn't working (wrong
	// port, wrong key, host not found...). TestKind and TestRef name the connection the row's
	// inline Test button checks (POST /api/v1/connections/{kind}/{ref}/test); both are empty
	// when nothing is configured yet, so there is nothing to test.
	Reason   string `json:"reason,omitempty"`
	TestKind string `json:"test_kind,omitempty"`
	TestRef  string `json:"test_ref,omitempty"`
}

type setupChecklist struct {
	Items     []checklistItem `json:"items"`
	Done      bool            `json:"done"` // every item ticked
	Dismissed bool            `json:"dismissed"`
}

// withProbe attaches a connection's Test target and, when it's been failing, the reason.
func (it checklistItem) withProbe(kind, ref, reason string) checklistItem {
	it.TestKind, it.TestRef, it.Reason = kind, ref, reason
	return it
}

func fixItem(key, label string, done bool, fix *health.Fix) checklistItem {
	return checklistItem{Key: key, Label: label, Done: done, Link: fix.Path, LinkKey: fix.Key}
}

// setupChecklist works out each step. A check that can't be answered (a service not wired,
// a failed read) counts as not done: the card then points at the page, which says more.
func (a *api) setupChecklist(ctx context.Context) setupChecklist {
	conns := a.connectionIndex(ctx)
	tmdbSet := a.deps.APIKeys != nil && a.deps.APIKeys.Value(ctx, "tmdb") != ""
	tmdb := fixItem("tmdb", "Add your TMDB key, for titles, artwork and Discover", tmdbSet, health.FixAPIKeys)
	if tmdbSet {
		tmdb = tmdb.withProbe(connKindAPIKey, "tmdb", failureReason(conns[connKey(connKindAPIKey, "tmdb")]))
	}
	dlOK, dlRef, dlReason := a.downloadClientProbe(ctx)
	dl := fixItem("download_client", "Connect a download client", dlOK, health.FixDownloadClients)
	if dlRef != "" {
		dl = dl.withProbe(connstatus.KindDownloadClient, dlRef, dlReason)
	}
	ixKind, ixRef, ixReason := a.indexerProbe(ctx, conns)
	ix := fixItem("indexer", "Add an indexer, or sync them from Prowlarr", a.enabledIndexers(ctx) > 0, health.FixIndexers)
	if ixRef != "" {
		ix = ix.withProbe(ixKind, ixRef, ixReason)
	}
	// Plex, Jellyfin or both: either one ticks it.
	ms := fixItem("media_server", "Connect your media server (Plex or Jellyfin)", a.plexConnected(ctx) || a.jellyfinConfigured(ctx) || a.embyConfigured(ctx), health.FixMediaServerCards)
	if kind := a.mediaServerKind(ctx); kind != "" {
		ms = ms.withProbe(kind, singletonRef, failureReason(conns[connKey(kind, singletonRef)]))
	}
	items := []checklistItem{
		tmdb,
		fixItem("folders", "Choose your library folders", a.librariesChosen(ctx), health.FixLibraryFolders),
		dl,
		ix,
		ms,
		{Key: "first_title", Label: "Add your first movie or show", Done: a.hasTitles(ctx), Link: "/discover"},
		{Key: "first_import", Label: "Get your first download into the library", Done: a.anyImported(ctx), Link: "/activity/history", LinkKey: "history"},
	}
	// The bundled SABnzbd comes without a news server: its step follows the client's.
	if it, ok := a.usenetProviderItem(ctx); ok {
		items = append(items[:3], append([]checklistItem{it}, items[3:]...)...)
	}
	out := setupChecklist{Items: items, Done: true, Dismissed: a.deps.Settings.GetBool(ctx, keyGettingStartedDismissed, false)}
	for _, it := range items {
		if !it.Done {
			out.Done = false
		}
	}
	return out
}

// downloadClientAnswers: at least one switched-on client answered its last queue read.
func (a *api) downloadClientAnswers(ctx context.Context) bool {
	ok, _, _ := a.downloadClientProbe(ctx)
	return ok
}

// downloadClientProbe reads the (shared, two-second-cached) queue snapshot under the 3 s
// bound: whether a switched-on client answered, which client the row's Test button should
// check (the first one that answered, else the first switched on) and, when none answered,
// why the first one didn't, in plain words.
func (a *api) downloadClientProbe(ctx context.Context) (ok bool, ref, reason string) {
	if a.deps.Downloads == nil {
		return false, "", ""
	}
	ctx, cancel := context.WithTimeout(ctx, checklistProbeTimeout)
	defer cancel()
	snap, err := a.deps.Downloads.Snapshot(ctx)
	if err != nil && len(snap.Health) == 0 {
		return false, "", ""
	}
	var first *download.ClientHealth
	for i, h := range snap.Health {
		if !h.Enabled {
			continue
		}
		if h.Reachable {
			return true, strconv.FormatInt(h.ID, 10), ""
		}
		if first == nil {
			first = &snap.Health[i]
		}
	}
	if first == nil {
		return false, "", ""
	}
	if first.LastErr != "" {
		_, reason = connstatus.ExplainText(first.LastErr)
	}
	return false, strconv.FormatInt(first.ID, 10), reason
}

// indexerProbe picks what the indexer row's Test checks: a switched-on indexer that isn't
// failing, else the first switched-on one (with its reason), else Prowlarr when it is set up.
// The reason comes from how the connection has been answering; nothing is called here.
func (a *api) indexerProbe(ctx context.Context, conns map[string]connectionItem) (kind, ref, reason string) {
	if a.deps.Indexers != nil {
		if list, err := a.deps.Indexers.List(ctx); err == nil {
			for _, ix := range list {
				if !ix.Enabled {
					continue
				}
				r := strconv.FormatInt(ix.ID, 10)
				why := failureReason(conns[connKey(connstatus.KindIndexer, r)])
				if why == "" {
					return connstatus.KindIndexer, r, ""
				}
				if ref == "" {
					ref, reason = r, why
				}
			}
			if ref != "" {
				return connstatus.KindIndexer, ref, reason
			}
		}
	}
	if it := conns[connKey(connstatus.KindProwlarr, prowlarrRef)]; it.Configured {
		return connstatus.KindProwlarr, prowlarrRef, failureReason(it)
	}
	return "", "", ""
}

// mediaServerKind is the configured media server the row's Test checks: Plex, else
// Jellyfin, else Emby; empty when none is set up.
func (a *api) mediaServerKind(ctx context.Context) string {
	switch {
	case a.plexConnected(ctx):
		return connKindPlex
	case a.jellyfinConfigured(ctx):
		return connKindJellyfin
	case a.embyConfigured(ctx):
		return connKindEmby
	}
	return ""
}

func (a *api) embyConfigured(ctx context.Context) bool {
	return a.deps.Emby != nil && a.deps.Emby.Configured(ctx)
}

func connKey(kind, ref string) string { return kind + "/" + ref }

// connectionIndex is every connection card by kind/ref, read from what's already known
// (the status tracker), never from the network.
func (a *api) connectionIndex(ctx context.Context) map[string]connectionItem {
	out := map[string]connectionItem{}
	sections, err := a.connectionSections(ctx, true)
	if err != nil {
		return out
	}
	for _, s := range sections {
		for _, it := range s.Items {
			out[connKey(it.Kind, it.Ref)] = it
		}
	}
	return out
}

// failureReason is the plain-words reason a configured connection is failing, from the
// error it last recorded; empty while it works, was never asked, or is switched off.
func failureReason(it connectionItem) string {
	if !it.Configured || (it.State != "failing" && it.State != "backing_off") || it.LastError == "" {
		return ""
	}
	_, text := connstatus.ExplainText(it.LastError)
	return text
}

// enabledIndexers counts the indexers searches can use.
func (a *api) enabledIndexers(ctx context.Context) int {
	if a.deps.Indexers == nil {
		return 0
	}
	list, err := a.deps.Indexers.List(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, ix := range list {
		if ix.Enabled {
			n++
		}
	}
	return n
}

// plexConnected: a server address and token are saved (the Dashboard's own test for it).
func (a *api) plexConnected(ctx context.Context) bool {
	if a.deps.Insights == nil {
		return false
	}
	cfg := a.deps.Insights.Config(ctx)
	return cfg.URL != "" && cfg.TokenSet
}

// jellyfinConfigured: a Jellyfin address and API key are saved (Plex's test, for Jellyfin).
func (a *api) jellyfinConfigured(ctx context.Context) bool {
	return a.deps.Jellyfin != nil && a.deps.Jellyfin.Configured(ctx)
}

// hasTitles: any movie or show in the library, added or scanned in.
func (a *api) hasTitles(ctx context.Context) bool {
	if a.deps.Store == nil {
		return false
	}
	var n int
	err := a.deps.Store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM movies) OR EXISTS(SELECT 1 FROM series)`).Scan(&n)
	return err == nil && n == 1
}

// anyImported: a download has been imported — a movie import that placed a file, or any
// grab marked imported (series and books record theirs there).
func (a *api) anyImported(ctx context.Context) bool {
	if a.deps.Library != nil {
		if recs, err := a.deps.Library.Recent(ctx, 1); err == nil && len(recs) > 0 {
			return true
		}
	}
	if a.deps.Automation != nil {
		if ok, err := a.deps.Automation.AnyImported(ctx); err == nil && ok {
			return true
		}
	}
	return false
}

// handleSetupChecklist — GET /api/v1/setup/checklist (admin). The first time every step
// reads as done, the card is put away for good: a step that later comes undone (Plex
// unplugged for a day) is a health warning's job, not a reason to bring "Getting started"
// back.
func (a *api) handleSetupChecklist(w http.ResponseWriter, r *http.Request) {
	c := a.setupChecklist(r.Context())
	if c.Done && !c.Dismissed {
		if err := a.deps.Settings.SetBool(r.Context(), keyGettingStartedDismissed, true); err == nil {
			c.Dismissed = true
		}
	}
	a.writeJSON(w, http.StatusOK, c)
}

// handleDismissSetupChecklist — POST /api/v1/setup/checklist/dismiss (admin). Hides the
// Dashboard card for good; the wizard's Finish step still shows the list.
func (a *api) handleDismissSetupChecklist(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Settings.SetBool(r.Context(), keyGettingStartedDismissed, true); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save")
		return
	}
	a.writeJSON(w, http.StatusOK, a.setupChecklist(r.Context()))
}
