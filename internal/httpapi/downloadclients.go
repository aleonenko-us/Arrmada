package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/scheduler"
)

// clientView is a download client as the settings page sees it: its settings (the
// password is never marshalled) and, when it has failed, since when and why.
type clientView struct {
	download.Client
	Status *clientStatus `json:"status,omitempty"`
}

// clientStatus is what the card says about a client that isn't answering. state is ok,
// failing or unknown (not asked since it was added or edited); last_error is redacted
// before it's stored.
type clientStatus struct {
	State        string     `json:"state"`
	FailingSince *time.Time `json:"failing_since,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
}

func (a *api) clientView(c download.Client) clientView {
	v := clientView{Client: c}
	if st, ok := a.deps.Downloads.Status(c.ID); ok {
		v.Status = &clientStatus{State: st.Phase(time.Now()), FailingSince: timePtr(st.FailingSince)}
		if st.ConsecutiveFailures > 0 {
			v.Status.LastError, v.Status.LastErrorAt = st.LastError, timePtr(st.LastErrorAt)
		}
	}
	return v
}

func (a *api) handleListDownloadClients(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Downloads.List(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list download clients")
		return
	}
	out := make([]clientView, 0, len(list))
	hasBundled := false
	for _, c := range list {
		out = append(out, a.clientView(c))
		hasBundled = hasBundled || c.Bundled
	}
	// The categories are Arrmada's, not the client's: the page lists them read-only so
	// the owner can see what to expect in qBittorrent. can_restore_bundled offers the
	// bundled qBittorrent back when this install has one and its row is gone.
	a.writeJSON(w, http.StatusOK, map[string]any{
		"clients":             out,
		"categories":          download.FixedCategories(a.deps.Config.DownloadCategory),
		"can_restore_bundled": a.deps.Config.QbittorrentURL != "" && !hasBundled,
	})
}

// maxClientPriority bounds the order field; anything past it is a typo.
const maxClientPriority = 99

// clientPriority checks an optional priority from a request: nil means keep (0 to the
// store), otherwise 1..maxClientPriority.
func clientPriority(p *int) (int, error) {
	if p == nil {
		return 0, nil
	}
	if *p < 1 || *p > maxClientPriority {
		return 0, fmt.Errorf("order must be between 1 and %d", maxClientPriority)
	}
	return *p, nil
}

// apiClientKinds are the client kinds the API lets someone add or edit, in the order the
// error message names them. A kind joins here once it has an implementation and a card.
var apiClientKinds = []download.Kind{download.KindQbittorrent, download.KindSABnzbd}

// checkClientKind refuses a kind the API doesn't offer.
func checkClientKind(kind string) error {
	for _, k := range apiClientKinds {
		if download.Kind(kind) == k {
			return nil
		}
	}
	names := make([]string, len(apiClientKinds))
	for i, k := range apiClientKinds {
		names[i] = "'" + string(k) + "'"
	}
	return fmt.Errorf("kind must be %s", strings.Join(names, " or "))
}

type createClientRequest struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Category is accepted so an older page's request still parses, and ignored:
	// Arrmada picks the category for every download (download/categories.go).
	Category string `json:"category"`
	Enabled  *bool  `json:"enabled"`
	// Priority is the client's place in the order new downloads try (1 = first); left
	// out, a new client gets the default and an edited one keeps its own.
	Priority *int `json:"priority"`
	// PathMaps replaces the client's remote path mappings; left out, an edited client
	// keeps its own (so an on/off toggle doesn't need to know them).
	PathMaps *[]download.PathMap `json:"path_maps"`
}

// clientPathMaps checks the mappings in a request. A client that is the bundled
// qBittorrent shares Arrmada's own folders, so it takes none.
func (a *api) clientPathMaps(req createClientRequest, bundled bool, keep download.PathMaps) (download.PathMaps, error) {
	if req.PathMaps == nil {
		return keep, nil
	}
	maps, err := download.ValidatePathMaps(*req.PathMaps, a.deps.Config.DataDir)
	if err != nil {
		return nil, err
	}
	if bundled && len(maps) > 0 {
		return nil, errors.New("the bundled qBittorrent shares Arrmada's folders, so it needs no path mapping")
	}
	return maps, nil
}

func (a *api) handleCreateDownloadClient(w http.ResponseWriter, r *http.Request) {
	var req createClientRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.URL == "" {
		a.writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	if err := checkClientKind(req.Kind); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	priority, err := clientPriority(req.Priority)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	maps, err := a.clientPathMaps(req, false, nil)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	created, err := a.deps.Downloads.Create(r.Context(), download.Client{
		Name:     req.Name,
		Kind:     download.Kind(req.Kind),
		URL:      req.URL,
		Username: req.Username,
		Password: req.Password,
		Enabled:  enabled,
		Priority: priority,
		PathMaps: maps,
	})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not create download client")
		return
	}
	a.recheckHealth(r, "downloads")
	if created.Enabled {
		a.kickProwlarrSync(created)
	}
	a.writeJSON(w, http.StatusCreated, a.clientView(created))
}

// kickProwlarrSync runs the Prowlarr sync now when a usenet client was just added or
// switched on: Prowlarr's usenet indexers only come in while one exists, and they
// shouldn't wait for the hourly run. Before Prowlarr is set up the sync does nothing.
func (a *api) kickProwlarrSync(c download.Client) {
	if a.deps.Scheduler == nil || c.Kind.Transport() != download.TransportUsenet {
		return
	}
	_, _, err := a.deps.Scheduler.RunNow("prowlarr-sync", scheduler.TriggerManual)
	if err != nil && !errors.Is(err, scheduler.ErrBusy) && !errors.Is(err, scheduler.ErrUnknownTask) && a.deps.Log != nil {
		a.deps.Log.Warn("couldn't start a Prowlarr sync for the new usenet client", "err", err)
	}
}

// handleUpdateDownloadClient edits a client in place — a changed password or URL no longer
// means delete and re-add. A blank password keeps the stored one; the stored one is never
// sent back. The bundled client's URL can be edited too: startup finds it by its bundled
// flag now, not its URL, so an edit no longer brings a duplicate back.
func (a *api) handleUpdateDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req createClientRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	req.Name, req.URL = strings.TrimSpace(req.Name), strings.TrimSpace(req.URL)
	if req.Name == "" || req.URL == "" {
		a.writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	if err := checkClientKind(req.Kind); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cur, err := a.deps.Downloads.Get(r.Context(), id)
	if errors.Is(err, download.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "download client not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read download client")
		return
	}
	// The kind is never changed by an edit (the row keeps its own); say so rather than
	// save the rest and leave a qBittorrent row answering as something else.
	if download.Kind(req.Kind) != cur.Kind {
		a.writeError(w, http.StatusBadRequest, "a download client's kind can't be changed — add a new client instead")
		return
	}
	enabled := cur.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	priority, err := clientPriority(req.Priority)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	maps, err := a.clientPathMaps(req, cur.Bundled, cur.PathMaps)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := a.deps.Downloads.Update(r.Context(), download.Client{
		ID:       id,
		Name:     req.Name,
		URL:      req.URL,
		Username: req.Username,
		Password: req.Password,
		Enabled:  enabled,
		Priority: priority,
		PathMaps: maps,
	})
	if errors.Is(err, download.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "download client not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not update download client")
		return
	}
	// The health panel's client check reads enabled clients; re-run it so switching one
	// off (or fixing its password) shows now rather than at the next scheduled check.
	a.recheckHealth(r, "downloads")
	if updated.Enabled && !cur.Enabled {
		a.kickProwlarrSync(updated)
	}
	a.writeJSON(w, http.StatusOK, a.clientView(updated))
}

func (a *api) handleDeleteDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Downloads.Delete(r.Context(), id); err != nil {
		if errors.Is(err, download.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "download client not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "could not delete download client")
		return
	}
	a.recheckHealth(r, "downloads")
	w.WriteHeader(http.StatusNoContent)
}

// handleRestoreBundledClient brings back the packaged qBittorrent after it was deleted:
// a deleted bundled client now stays deleted across restarts, so this is the way back.
func (a *api) handleRestoreBundledClient(w http.ResponseWriter, r *http.Request) {
	url := a.deps.Config.QbittorrentURL
	if url == "" {
		a.writeError(w, http.StatusBadRequest, "this install has no bundled qBittorrent")
		return
	}
	if err := a.deps.Downloads.RestoreBundled(r.Context(), url); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not restore the bundled qBittorrent")
		return
	}
	a.recheckHealth(r, "downloads")
	a.writeJSON(w, http.StatusOK, map[string]any{"restored": true})
}

// handleTestDownloadClient logs in to a client and, once that works, checks its paths:
// where it saves, whether Arrmada can see that and its newest finished download, and a
// mapping that would fix it. ok is the connection; paths.ok is whether Arrmada can see
// what it downloads.
func (a *api) handleTestDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	err := a.deps.Downloads.Test(r.Context(), id)
	if errors.Is(err, download.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "download client not found")
		return
	}
	if err != nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	out := map[string]any{"ok": true}
	if rep, perr := a.clientPaths(r.Context(), id); perr == nil {
		out["paths"] = rep
	} else if !errors.Is(perr, download.ErrNoPathCheck) {
		out["paths_error"] = connstatus.Redact(perr.Error())
	}
	a.writeJSON(w, http.StatusOK, out)
}

// clientPaths runs a client's path check against Arrmada's folders as they are now.
func (a *api) clientPaths(ctx context.Context, id int64) (download.PathReport, error) {
	roots := a.roots()
	env := download.PathEnv{Downloads: roots.Downloads(ctx), DataDir: a.deps.Config.DataDir}
	for _, lib := range roots.Libraries(ctx) {
		env.Libraries = append(env.Libraries, lib.Path)
	}
	return a.deps.Downloads.TestPaths(ctx, id, env)
}

// handleDownloadClientStatus reports live client info: the incoming BitTorrent port, so the
// UI can tell the user which port to forward, and for a usenet client what its card shows
// (usenet: categories present, completed folder and whether Arrmada sees it, news servers,
// language; usenet_error when it couldn't be read). Only reads.
func (a *api) handleDownloadClientStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	out := map[string]any{"listen_port": 0}
	// Not fatal: the client may be briefly unreachable. Report 0.
	if port, err := a.deps.Downloads.ListenPort(r.Context(), id); err == nil {
		out["listen_port"] = port
	}
	facts, err := a.deps.Downloads.UsenetFacts(r.Context(), id)
	switch {
	case err == nil:
		out["usenet"] = facts
	case errors.Is(err, download.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "download client not found")
		return
	case !errors.Is(err, download.ErrNotUsenet):
		out["usenet_error"] = connstatus.Redact(err.Error())
	}
	a.writeJSON(w, http.StatusOK, out)
}

// handleAddUsenetCategories creates the Arrmada categories a usenet client lacks. It's the
// card's "Add missing categories" button and nothing else calls it: Arrmada never changes
// a SABnzbd it was pointed at on its own. Answers the categories it added and the card's
// facts as they are now.
func (a *api) handleAddUsenetCategories(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	created, err := a.deps.Downloads.AddMissingCategories(r.Context(), id)
	switch {
	case errors.Is(err, download.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "download client not found")
		return
	case errors.Is(err, download.ErrNotUsenet):
		a.writeError(w, http.StatusBadRequest, "only a usenet client has categories to add")
		return
	case err != nil:
		a.writeError(w, http.StatusBadGateway, connstatus.Redact(err.Error()))
		return
	}
	out := map[string]any{"created": created}
	if facts, ferr := a.deps.Downloads.UsenetFacts(r.Context(), id); ferr == nil {
		out["usenet"] = facts
	}
	a.recheckHealth(r, "downloads")
	a.writeJSON(w, http.StatusOK, out)
}

func (a *api) handleQueue(w http.ResponseWriter, r *http.Request) {
	items, err := a.deps.Downloads.Queue(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read queue")
		return
	}
	if items == nil {
		items = []download.Item{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// unseenSample is how many of a client's newest finished downloads the paths check
// looks at: enough to tell a missing mount from one deleted torrent, few enough that a
// client seeding hundreds doesn't mean hundreds of disk reads a minute.
const unseenSample = 20

// unseenDownloads is, per client other than the bundled one, the finished downloads in
// the shared queue snapshot whose (mapped) path isn't on disk here. The bundled client
// saves into Arrmada's own downloads folder and is left out; a torrent the client itself
// says has lost its files is too, since no mapping would bring it back.
func (a *api) unseenDownloads(ctx context.Context) ([]health.UnseenDownloads, error) {
	clients, err := a.deps.Downloads.List(ctx)
	if err != nil {
		return nil, err
	}
	watch := map[int64]download.Client{}
	for _, c := range clients {
		if !c.Bundled {
			watch[c.ID] = c
		}
	}
	if len(watch) == 0 {
		return nil, nil
	}
	snap, err := a.deps.Downloads.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	byClient := map[int64][]download.Item{}
	for _, it := range snap.Items {
		if _, ok := watch[it.ClientID]; !ok || !it.Complete() || it.ContentPath == "" || it.RawState == "missingFiles" {
			continue
		}
		byClient[it.ClientID] = append(byClient[it.ClientID], it)
	}
	var out []health.UnseenDownloads
	for id, items := range byClient {
		sort.Slice(items, func(i, j int) bool { return items[i].AddedOn > items[j].AddedOn })
		if len(items) > unseenSample {
			items = items[:unseenSample]
		}
		u := health.UnseenDownloads{ClientID: id, ClientName: watch[id].Name}
		for _, it := range items {
			if filepath.IsAbs(it.ContentPath) {
				if _, err := os.Stat(it.ContentPath); err == nil {
					continue
				}
			}
			if u.Count == 0 {
				u.Example = it.ContentPath
			}
			u.Count++
		}
		if u.Count > 0 {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out, nil
}
