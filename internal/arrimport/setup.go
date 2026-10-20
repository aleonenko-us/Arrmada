package arrimport

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// The other half of leaving Radarr or Sonarr: their indexers and download clients. Both are
// read with plain GETs and brought over additively:
//
//   - Radarr and Sonarr never hand out secrets (their API answers "********" for an API key
//     or password), so anything that needs one comes over switched off, with a note saying
//     what to fill in. Nothing is ever enabled that Arrmada could not reach with what it has.
//   - A row Arrmada already has (the same address) is left exactly as it is; running the
//     import twice, or once per Radarr and Sonarr that share an indexer, adds nothing twice.
//   - Nothing is deleted or edited, and nothing is searched or downloaded.

// maskedSecret is what Radarr and Sonarr answer in place of a stored secret.
const maskedSecret = "********"

// field is one entry of an app's "fields" list (its settings form).
type field struct {
	Name    string          `json:"name"`
	Value   json.RawMessage `json:"value"`
	Privacy string          `json:"privacy"`
}

type fields []field

func (fs fields) get(name string) (field, bool) {
	for _, f := range fs {
		if f.Name == name {
			return f, true
		}
	}
	return field{}, false
}

func (fs fields) str(name string) string {
	f, ok := fs.get(name)
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(f.Value, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(f.Value, &n) == nil {
		return n.String()
	}
	return ""
}

func (fs fields) num(name string) float64 {
	f, ok := fs.get(name)
	if !ok {
		return 0
	}
	var n float64
	if json.Unmarshal(f.Value, &n) == nil {
		return n
	}
	return 0
}

func (fs fields) boolean(name string) bool {
	f, ok := fs.get(name)
	if !ok {
		return false
	}
	var b bool
	_ = json.Unmarshal(f.Value, &b)
	return b
}

func (fs fields) ints(name string) []int {
	f, ok := fs.get(name)
	if !ok {
		return nil
	}
	var out []int
	_ = json.Unmarshal(f.Value, &out)
	return out
}

// secret is a secret field's value: ("", true) when the app keeps one it won't show.
func (fs fields) secret(name string) (value string, hidden bool) {
	if _, ok := fs.get(name); !ok {
		return "", false
	}
	v := fs.str(name)
	if v == maskedSecret {
		return "", true
	}
	return v, false
}

// ArrIndexer is one of the app's indexers, as read.
type ArrIndexer struct {
	ID                      int    `json:"id"`
	Name                    string `json:"name"`
	Implementation          string `json:"implementation"`
	Protocol                string `json:"protocol"`
	Priority                int    `json:"priority"`
	EnableRss               bool   `json:"enableRss"`
	EnableAutomaticSearch   bool   `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool   `json:"enableInteractiveSearch"`
	Fields                  fields `json:"fields"`
}

// ArrDownloadClient is one of the app's download clients, as read.
type ArrDownloadClient struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
	Protocol       string `json:"protocol"`
	Priority       int    `json:"priority"`
	Enable         bool   `json:"enable"`
	Fields         fields `json:"fields"`
}

// Indexers lists the app's indexers.
func (c *Client) Indexers(ctx context.Context) ([]ArrIndexer, error) {
	var list []ArrIndexer
	err := c.get(ctx, "/api/v3/indexer", &list)
	return list, err
}

// DownloadClients lists the app's download clients.
func (c *Client) DownloadClients(ctx context.Context) ([]ArrDownloadClient, error) {
	var list []ArrDownloadClient
	err := c.get(ctx, "/api/v3/downloadclient", &list)
	return list, err
}

// SetupSnapshot is the app's indexers and download clients as one read saw them.
type SetupSnapshot struct {
	Indexers []ArrIndexer
	Clients  []ArrDownloadClient
}

// LoadSetup reads the app's indexers and download clients.
func LoadSetup(ctx context.Context, c *Client) (*SetupSnapshot, error) {
	ix, err := c.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	dc, err := c.DownloadClients(ctx)
	if err != nil {
		return nil, err
	}
	return &SetupSnapshot{Indexers: ix, Clients: dc}, nil
}

// What would happen to one indexer or client.
const (
	ActionAdd    = "add"    // would be added
	ActionExists = "exists" // Arrmada has it already: left as it is
	ActionSkip   = "skip"   // can't be brought over (Reason says why)
	ActionAdded  = "added"  // result: added
	ActionFailed = "failed" // result: Arrmada refused or couldn't store it
)

// SetupItem is one indexer or download client in the preview and the result.
type SetupItem struct {
	Name string `json:"name"`
	// Kind is what it becomes in Arrmada ("torznab", "newznab", "qbittorrent", "sabnzbd");
	// "" when it can't come over. Source is what it is over there ("Torznab", "Deluge").
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Address is where it is (never carries a key or password).
	Address string `json:"address,omitempty"`
	Action  string `json:"action"`
	// Reason says why it is skipped, left alone, or what's left to do after adding it.
	Reason string `json:"reason,omitempty"`
	// Enabled says it comes over switched on; false: switched off until the person adds
	// what the app wouldn't share.
	Enabled bool `json:"enabled"`

	// What the store is given, never serialised.
	idx *indexer.Indexer
	dc  *download.Client
}

// SetupPlan is what bringing the indexers and download clients over would do.
type SetupPlan struct {
	Indexers []SetupItem `json:"indexers"`
	Clients  []SetupItem `json:"clients"`
}

// Count is how many items have the action.
func Count(items []SetupItem, action string) int {
	n := 0
	for _, it := range items {
		if it.Action == action {
			n++
		}
	}
	return n
}

// IndexerStore is what the import needs of Arrmada's indexers (indexer.Service).
type IndexerStore interface {
	List(ctx context.Context) ([]indexer.Indexer, error)
	Create(ctx context.Context, idx indexer.Indexer) (indexer.Indexer, error)
}

// ClientStore is what the import needs of Arrmada's download clients (download.Service).
type ClientStore interface {
	List(ctx context.Context) ([]download.Client, error)
	Create(ctx context.Context, c download.Client) (download.Client, error)
}

// addressKey compares addresses loosely: scheme, host and port (default ports spelled out)
// and path, lower-case, no trailing slash, no query (a Torznab key rides in the query).
func addressKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "/"))
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		}
	}
	return strings.ToLower(u.Scheme + "://" + u.Hostname() + ":" + port + strings.TrimRight(u.EscapedPath(), "/"))
}

// isProwlarrManaged: Prowlarr names what it syncs into an app "<name> (Prowlarr)".
func isProwlarrManaged(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), "(prowlarr)")
}

func joinURL(base, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	path = strings.TrimSpace(path)
	if path == "" {
		return base
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

// planIndexer works out one of the app's indexers: its Arrmada row, or why it can't be one.
func planIndexer(ai ArrIndexer, media string) SetupItem {
	it := SetupItem{Name: strings.TrimSpace(ai.Name), Source: ai.Implementation}
	if it.Name == "" {
		it.Name = ai.Implementation
	}
	impl := strings.ToLower(strings.TrimSpace(ai.Implementation))
	if isProwlarrManaged(ai.Name) {
		it.Action = ActionSkip
		it.Reason = "Synced from Prowlarr. Connect Prowlarr in Arrmada (Settings → Connections) and it brings the same indexers over."
		return it
	}
	var kind indexer.Kind
	switch impl {
	case "torznab":
		kind = indexer.KindTorznab
	case "newznab":
		kind = indexer.KindNewznab
	case "torrentleech":
		it.Action = ActionSkip
		it.Reason = "Arrmada has its own TorrentLeech integration that signs in with your username and password: add it in Settings → Connections."
		return it
	default:
		it.Action = ActionSkip
		it.Reason = fmt.Sprintf("%s is built into the app. Arrmada can't read it from there: add it through Prowlarr or its Torznab address.", ai.Implementation)
		return it
	}
	base := ai.Fields.str("baseUrl")
	if base == "" {
		it.Action = ActionSkip
		it.Reason = "It has no address."
		return it
	}
	api := ai.Fields.str("apiPath")
	if api == "" {
		api = "/api"
	}
	addr := joinURL(base, api)
	key, hidden := ai.Fields.secret("apiKey")
	row := &indexer.Indexer{
		Name:       it.Name,
		Kind:       kind,
		URL:        addr,
		APIKey:     key,
		Categories: ai.Fields.ints("categories"),
		MediaTypes: []string{media},
		Priority:   clampPriority(ai.Priority),
		MinSeeders: int(ai.Fields.num("minimumSeeders")),
	}
	if kind == indexer.KindTorznab {
		row.SeedEnabled = true
		row.SeedRatio = ai.Fields.num("seedCriteria.seedRatio")
		if mins := ai.Fields.num("seedCriteria.seedTime"); mins > 0 {
			row.SeedHours = int(math.Ceil(mins / 60))
		}
		if row.SeedRatio == 0 && row.SeedHours == 0 {
			row.SeedHours = indexer.DefaultSeedHours
		}
	}
	appOn := ai.EnableRss || ai.EnableAutomaticSearch || ai.EnableInteractiveSearch
	switch {
	case hidden:
		row.Enabled = false
		row.DisabledBy = indexer.DisabledByUser
		row.ManagedNote = "Imported from the app: add its API key, test it, then switch it on"
		it.Reason = "Added switched off: the app doesn't share its API key. Add the key in Settings → Connections, then switch it on."
	case !appOn:
		row.Enabled = false
		row.DisabledBy = indexer.DisabledByUser
		it.Reason = "Added switched off, as it is in the app."
	default:
		row.Enabled = true
	}
	it.Kind, it.Address, it.Action, it.Enabled = string(kind), redactAddr(addr), ActionAdd, row.Enabled
	it.idx = row
	return it
}

func clampPriority(p int) int {
	if p < 1 || p > 50 {
		return 25
	}
	return p
}

// redactAddr drops a password and any query (a key may ride in it) from an address shown
// to the person.
func redactAddr(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery, u.User = "", nil
	return u.String()
}

// planClient works out one of the app's download clients.
func planClient(ac ArrDownloadClient) SetupItem {
	it := SetupItem{Name: strings.TrimSpace(ac.Name), Source: ac.Implementation}
	if it.Name == "" {
		it.Name = ac.Implementation
	}
	var kind download.Kind
	switch strings.ToLower(strings.TrimSpace(ac.Implementation)) {
	case "qbittorrent":
		kind = download.KindQbittorrent
	case "sabnzbd":
		kind = download.KindSABnzbd
	default:
		it.Action = ActionSkip
		it.Reason = fmt.Sprintf("Arrmada works with qBittorrent and SABnzbd, not %s.", ac.Implementation)
		return it
	}
	host := ac.Fields.str("host")
	if host == "" {
		it.Action = ActionSkip
		it.Reason = "It has no host."
		return it
	}
	scheme := "http"
	if ac.Fields.boolean("useSsl") {
		scheme = "https"
	}
	addr := scheme + "://" + host
	if p := int(ac.Fields.num("port")); p > 0 {
		addr += ":" + strconv.Itoa(p)
	}
	if b := strings.Trim(ac.Fields.str("urlBase"), "/ "); b != "" {
		addr += "/" + b
	}
	row := &download.Client{Name: it.Name, Kind: kind, URL: addr, Priority: clampPriority(ac.Priority)}
	var hidden bool
	if kind == download.KindSABnzbd {
		// SABnzbd's secret is its API key, which Arrmada keeps where other clients keep a
		// password.
		row.Password, hidden = ac.Fields.secret("apiKey")
		row.Username = ac.Fields.str("username")
	} else {
		row.Username = ac.Fields.str("username")
		row.Password, hidden = ac.Fields.secret("password")
	}
	switch {
	case hidden:
		it.Reason = "Added switched off: the app doesn't share its password or API key. Enter it in Settings → Connections, test it, then switch it on."
	case !ac.Enable:
		it.Reason = "Added switched off, as it is in the app."
	default:
		row.Enabled = true
	}
	it.Kind, it.Address, it.Action, it.Enabled = string(kind), redactAddr(addr), ActionAdd, row.Enabled
	it.dc = row
	return it
}

// PlanSetup works out what bringing the app's indexers and download clients over would do
// against what Arrmada has now. It reads only.
func (imp *Importer) PlanSetup(ctx context.Context, snap *Snapshot, ss *SetupSnapshot) (SetupPlan, error) {
	plan := SetupPlan{Indexers: []SetupItem{}, Clients: []SetupItem{}}
	if ss == nil {
		return plan, nil
	}
	media := snap.Media()
	if imp.Indexers != nil {
		have, err := imp.Indexers.List(ctx)
		if err != nil {
			return plan, fmt.Errorf("couldn't read Arrmada's indexers: %w", err)
		}
		urls, names := map[string]bool{}, map[string]bool{}
		for _, h := range have {
			urls[addressKey(h.URL)] = true
			names[strings.ToLower(h.Name)] = true
		}
		for _, ai := range ss.Indexers {
			it := planIndexer(ai, media)
			if it.idx != nil {
				key := addressKey(it.idx.URL)
				switch {
				case urls[key]:
					it.Action, it.Reason, it.Enabled, it.idx = ActionExists, "Arrmada already has an indexer at this address; it is left as it is.", false, nil
				default:
					urls[key] = true
					// Arrmada finds an indexer by name when it fetches a release: names stay unique.
					if names[strings.ToLower(it.idx.Name)] {
						it.idx.Name += " (from " + snap.Status.Name(snap.App) + ")"
						it.Name = it.idx.Name
					}
					names[strings.ToLower(it.idx.Name)] = true
				}
			}
			plan.Indexers = append(plan.Indexers, it)
		}
	}
	if imp.Clients != nil {
		have, err := imp.Clients.List(ctx)
		if err != nil {
			return plan, fmt.Errorf("couldn't read Arrmada's download clients: %w", err)
		}
		urls := map[string]bool{}
		for _, h := range have {
			urls[string(h.Kind)+" "+addressKey(h.URL)] = true
		}
		for _, ac := range ss.Clients {
			it := planClient(ac)
			if it.dc != nil {
				key := string(it.dc.Kind) + " " + addressKey(it.dc.URL)
				if urls[key] {
					it.Action, it.Reason, it.Enabled, it.dc = ActionExists, "Arrmada already has this client; it is left as it is.", false, nil
				} else {
					urls[key] = true
				}
			}
			plan.Clients = append(plan.Clients, it)
		}
	}
	return plan, nil
}

// SetupResult is what bringing the indexers and download clients over did.
type SetupResult struct {
	Indexers []SetupItem `json:"indexers"`
	Clients  []SetupItem `json:"clients"`
}

// ApplySetup adds what the plan says to add. A row that Arrmada refuses is noted as failed
// and the rest carry on.
func (imp *Importer) ApplySetup(ctx context.Context, plan SetupPlan) (SetupResult, error) {
	res := SetupResult{Indexers: []SetupItem{}, Clients: []SetupItem{}}
	for _, it := range plan.Indexers {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if it.idx != nil && imp.Indexers != nil {
			if _, err := imp.Indexers.Create(ctx, *it.idx); err != nil {
				imp.log().Warn("arr import: couldn't add an indexer", "name", it.Name, "err", err)
				it.Action, it.Reason, it.Enabled = ActionFailed, "Arrmada couldn't save it.", false
			} else {
				it.Action = ActionAdded
			}
		}
		res.Indexers = append(res.Indexers, it)
	}
	for _, it := range plan.Clients {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if it.dc != nil && imp.Clients != nil {
			if _, err := imp.Clients.Create(ctx, *it.dc); err != nil {
				imp.log().Warn("arr import: couldn't add a download client", "name", it.Name, "err", err)
				it.Action, it.Reason, it.Enabled = ActionFailed, "Arrmada couldn't save it.", false
			} else {
				it.Action = ActionAdded
			}
		}
		res.Clients = append(res.Clients, it)
	}
	return res, nil
}
