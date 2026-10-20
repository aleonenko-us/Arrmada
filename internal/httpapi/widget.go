package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/buildinfo"
	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/netutil"
	"github.com/tristenlammi/arrmada/internal/safego"
)

// The dashboard widget: a small read-only summary for gethomepage.dev (or any dashboard
// with a "custom API" widget), so a homelab start page can show Arrmada's numbers next to
// everything else's. docs/homepage.md has the services.yaml snippet.
//
// Homepage fetches the widget from its own server, not the browser, and sends whatever
// headers its config names. So the route is opened by a key of its own:
//
//   - The widget key is one per install, made by an admin on Settings → System. It's 256
//     random bits; only its SHA-256 is kept (in settings), so it can't be read back from the
//     database, a backup or a support bundle. Making a new one stops the old at once.
//   - It opens this one route and nothing else. It isn't an account's API key: the sign-in
//     middleware never matches it, so it can't call any other route, and a person's session
//     or API key — requester, manager or admin — can't open the widget either.
//   - The answer is counts only: how many titles, how much is wanted, the queue by phase,
//     how many requests wait, how much needs someone, disk space, and the version. Never a
//     title, a username, a path or anything about what anyone watches or listens to.
//   - It's sent as the X-Api-Key header (or Authorization: Bearer), never in the address, so
//     it stays out of request logs. Like most of the app it's LAN-only; Homepage normally
//     runs beside Arrmada.
//   - Only wrong keys count against an address (widgetFailMax a minute), so a misconfigured
//     client on the LAN can't use up Homepage's turns. A wrong key gets the same 401
//     whether or not a key has been made.
//   - The answer is kept for widgetTTL and worked out once for everyone waiting, so a
//     dashboard open in many tabs costs one set of reads, and a disk that hangs (a dead
//     network mount) holds up neither the answer nor more readings piling up behind it.

const (
	// keyWidgetToken is the settings key holding "<created unix>:<sha256 hex>" — one value,
	// so a new key and its time are written together. "token" keeps it out of support
	// bundles (support.go).
	keyWidgetToken = "widget_token"
	// widgetTokenPrefix marks a widget key apart from an account API key ("arr_").
	widgetTokenPrefix = "arrw_"
	widgetTokenBytes  = 32
	// widgetFailMax is how many wrong keys one address may send a minute before it's told
	// to wait. A right key is never limited (the cache makes it cheap).
	widgetFailMax = 30
	// widgetTimeout bounds working out one answer: the download client is a live call.
	widgetTimeout = 5 * time.Second
	// widgetTTL is how long one answer is reused. Homepage polls every 10–60 s per tab.
	widgetTTL = 15 * time.Second
	// widgetDiskWait is how long an answer waits for the disk readings before using the
	// last ones (or none).
	widgetDiskWait = 2 * time.Second
)

// widgetPlaceholderOrigin stands in for Arrmada's address in the services.yaml entry when
// the admin is on an outside address, which Homepage can't use (the widget is LAN-only).
const widgetPlaceholderOrigin = "http://ARRMADA-LAN-ADDRESS:PORT"

// widgetRuntime is the widget's cached answer and its disk prober.
type widgetRuntime struct {
	mu       sync.Mutex
	at       time.Time      // when val was worked out; zero = never
	val      homepageWidget // the last answer
	inflight chan struct{}  // closed when the answer being worked out is ready

	disk widgetDiskProbe
	// volumes measures the library disks; nil = storageVolumes. Tests swap it.
	volumes func(context.Context) []storageVolume
}

// widgetDiskProbe runs the disk readings off the request, one at a time.
type widgetDiskProbe struct {
	mu      sync.Mutex
	running chan struct{} // closed when the reading in progress finishes; nil = none
	last    widgetDisk
}

func hashWidgetToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// widgetKeyRecord is the saved key: its hash and when it was made. ok is false when no key
// is set.
func (a *api) widgetKeyRecord(ctx context.Context) (created int64, hash string, ok bool) {
	if a.deps.Settings == nil {
		return 0, "", false
	}
	at, h, found := strings.Cut(a.deps.Settings.Get(ctx, keyWidgetToken, ""), ":")
	if !found || len(h) != sha256.Size*2 {
		return 0, "", false
	}
	created, _ = strconv.ParseInt(at, 10, 64)
	return created, h, true
}

// widgetKeyState is GET /system/widget-key: whether a key exists and since when — never
// the key, which isn't stored. Outside says the admin is on an outside address, which
// can't go in Homepage's config.
type widgetKeyState struct {
	Enabled   bool  `json:"enabled"`
	CreatedAt int64 `json:"created_at,omitempty"`
	Outside   bool  `json:"outside"`
}

func (a *api) widgetKeyState(r *http.Request) widgetKeyState {
	st := widgetKeyState{Outside: a.classifyExternal(r)}
	if at, _, ok := a.widgetKeyRecord(r.Context()); ok {
		st.Enabled, st.CreatedAt = true, at
	}
	return st
}

// widgetKeyFromRequest is the widget key a request presents in X-Api-Key or
// Authorization: Bearer ("arrw_…"), or "". It is its own reader: a personal API key
// ("arrk_…") in the same headers is the account middleware's business, and never opens
// the widget.
func widgetKeyFromRequest(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-Api-Key")); strings.HasPrefix(k, widgetTokenPrefix) {
		return k
	}
	if scheme, cred, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " "); ok && strings.EqualFold(scheme, "Bearer") {
		if k := strings.TrimSpace(cred); strings.HasPrefix(k, widgetTokenPrefix) {
			return k
		}
	}
	return ""
}

// widgetKeyValid reports whether key is the install's widget key.
func (a *api) widgetKeyValid(ctx context.Context, key string) bool {
	if !strings.HasPrefix(key, widgetTokenPrefix) || len(key) > 128 {
		return false
	}
	_, want, ok := a.widgetKeyRecord(ctx)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashWidgetToken(key)), []byte(want)) == 1
}

// handleWidgetKey is GET /api/v1/system/widget-key (admin).
func (a *api) handleWidgetKey(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.widgetKeyState(r))
}

// handleRotateWidgetKey makes a new widget key, replacing any earlier one, and answers with
// it — the only time it's shown — and the widget's address as this request reached it (a
// placeholder when that's an outside address, which Homepage can't use).
func (a *api) handleRotateWidgetKey(w http.ResponseWriter, r *http.Request) {
	if a.deps.Settings == nil {
		a.writeError(w, http.StatusInternalServerError, "settings unavailable")
		return
	}
	b := make([]byte, widgetTokenBytes)
	if _, err := rand.Read(b); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not make a widget key")
		return
	}
	key := widgetTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	now := time.Now().Unix()
	// One write: the key and its time can't be left half-changed by a failure or by another
	// admin's reset or turn-off at the same moment.
	if err := a.deps.Settings.Set(r.Context(), keyWidgetToken, strconv.FormatInt(now, 10)+":"+hashWidgetToken(key)); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the widget key")
		return
	}
	outside := a.classifyExternal(r)
	origin := baseURL(r)
	if outside {
		origin = widgetPlaceholderOrigin
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "created_at": now, "key": key, "outside": outside,
		"url": origin + "/api/v1/widget/homepage",
	})
}

// handleRevokeWidgetKey turns the widget key off; dashboards using it get 401.
func (a *api) handleRevokeWidgetKey(w http.ResponseWriter, r *http.Request) {
	if a.deps.Settings == nil {
		a.writeError(w, http.StatusInternalServerError, "settings unavailable")
		return
	}
	if err := a.deps.Settings.Set(r.Context(), keyWidgetToken, ""); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not turn the widget key off")
		return
	}
	a.writeJSON(w, http.StatusOK, widgetKeyState{Outside: a.classifyExternal(r)})
}

// homepageWidget is GET /api/v1/widget/homepage. Every field is a number, a yes/no or a
// fixed word: the privacy test walks the answer and fails on any other string.
type homepageWidget struct {
	Movies   int `json:"movies"`
	Shows    int `json:"shows"`
	Episodes int `json:"episodes"`
	Books    int `json:"books"`
	// Music: library artists, albums with a track on disk and tracks on disk. All 0 while
	// the Music module is off.
	Artists int `json:"artists"`
	Albums  int `json:"albums"`
	Tracks  int `json:"tracks"`
	// Wanted is what's monitored and still missing (the Wanted page's searchable titles).
	Wanted widgetWanted `json:"wanted"`
	Queue  widgetQueue  `json:"queue"`
	// RequestsPending is requests waiting for approval; NeedsYou everything on Needs you
	// (requests included). Both as of the last Needs-you refresh, usually under a minute.
	RequestsPending int          `json:"requests_pending"`
	NeedsYou        int          `json:"needs_you"`
	Health          widgetHealth `json:"health"`
	Disk            widgetDisk   `json:"disk"`
	Version         string       `json:"version"`
	// UpdateAvailable is the System → Status "Update available" answer (false while that
	// check is off); LatestVersion the newest release it knows of.
	UpdateAvailable bool   `json:"update_available"`
	LatestVersion   string `json:"latest_version"`
}

type widgetWanted struct {
	Movies   int `json:"movies"`
	Episodes int `json:"episodes"`
	Books    int `json:"books"`
	Albums   int `json:"albums"` // released albums Music is searching for; 0 while Music is off
	Total    int `json:"total"`
}

type widgetQueue struct {
	// OK is false when the download client couldn't be read; the counts are then 0.
	OK          bool  `json:"ok"`
	Total       int   `json:"total"`
	Downloading int   `json:"downloading"`
	Seeding     int   `json:"seeding"`
	Stalled     int   `json:"stalled"`
	Paused      int   `json:"paused"`
	Errored     int   `json:"errored"`
	DownSpeed   int64 `json:"down_speed"` // bytes per second
	UpSpeed     int64 `json:"up_speed"`
}

type widgetHealth struct {
	Status   string `json:"status"` // ok | warning | error
	Problems int    `json:"problems"`
	Errors   int    `json:"errors"`
	Warnings int    `json:"warnings"`
}

// widgetDisk is the library and download disks, each filesystem once. Free/Total add them
// up; LowestFree* is the fullest one, the one about to cause trouble. OK is false when no
// disk could be read yet (a reading that hangs leaves the last one in place).
type widgetDisk struct {
	OK              bool    `json:"ok"`
	FreeBytes       uint64  `json:"free_bytes"`
	TotalBytes      uint64  `json:"total_bytes"`
	LowestFreeBytes uint64  `json:"lowest_free_bytes"`
	LowestFreePct   float64 `json:"lowest_free_pct"`
}

// handleHomepageWidget is GET /api/v1/widget/homepage, opened only by the widget key.
func (a *api) handleHomepageWidget(w http.ResponseWriter, r *http.Request) {
	limitKey := "widget:" + netutil.ClientIP(r)
	if l := a.widgetLimiter; l != nil {
		if blocked, retry := l.blocked(limitKey); blocked {
			a.tooManyAttempts(w, retry)
			return
		}
	}
	if !a.widgetKeyValid(r.Context(), widgetKeyFromRequest(r)) {
		if l := a.widgetLimiter; l != nil {
			l.fail(limitKey, "")
		}
		a.writeError(w, http.StatusUnauthorized, "a valid widget key is required (Settings → System → Dashboard widget)")
		return
	}
	out, ok := a.cachedWidget(r.Context())
	if !ok {
		a.writeError(w, http.StatusServiceUnavailable, "the widget is busy — try again in a moment")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, http.StatusOK, out)
}

// cachedWidget is the answer, reused for widgetTTL. Only one caller works it out at a time;
// the rest wait for that one (or give up with their own request). ok is false when the
// caller's request ended first.
func (a *api) cachedWidget(ctx context.Context) (homepageWidget, bool) {
	c := &a.widget
	for {
		c.mu.Lock()
		if !c.at.IsZero() && time.Since(c.at) < widgetTTL {
			v := c.val
			c.mu.Unlock()
			return v, true
		}
		if ch := c.inflight; ch != nil {
			c.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return homepageWidget{}, false
			}
		}
		ch := make(chan struct{})
		c.inflight = ch
		c.mu.Unlock()
		return a.refreshWidget(ch), true
	}
}

// refreshWidget works out a fresh answer and stores it, then wakes everyone waiting on ch.
// It runs on the app's context, not the request's, so the first caller hanging up doesn't
// spoil the answer for the others.
func (a *api) refreshWidget(ch chan struct{}) (v homepageWidget) {
	c := &a.widget
	stored := false
	defer func() {
		c.mu.Lock()
		if stored {
			c.val, c.at = v, time.Now()
		}
		c.inflight = nil
		c.mu.Unlock()
		close(ch)
	}()
	ctx, cancel := context.WithTimeout(a.runCtx(), widgetTimeout)
	defer cancel()
	v = a.homepageWidget(ctx)
	stored = true
	return v
}

func (a *api) homepageWidget(ctx context.Context) homepageWidget {
	lc := a.libraryCounts(ctx)
	out := homepageWidget{
		Movies: lc.Movies, Shows: lc.Series, Episodes: lc.Episodes, Books: lc.Books,
		Wanted: widgetWanted{
			Movies: lc.MoviesMissing, Episodes: lc.EpisodesMissing, Books: lc.BooksMissing,
			Total: lc.MoviesMissing + lc.EpisodesMissing + lc.BooksMissing,
		},
		Version: buildinfo.Version,
		Disk:    a.widgetDisk(),
	}
	if a.musicOn(ctx) {
		mc := a.musicCounts(ctx)
		out.Artists, out.Albums, out.Tracks = mc.Artists, mc.Albums, mc.Tracks
		out.Wanted.Albums = mc.AlbumsMissing
		out.Wanted.Total += mc.AlbumsMissing
	}

	if a.deps.Downloads != nil {
		if items, err := a.deps.Downloads.Queue(ctx); err == nil {
			q := summarizeQueue(items, a.queueStallJudge(ctx))
			out.Queue = widgetQueue{
				OK: true, Total: len(items),
				Downloading: q.Downloading, Seeding: q.Seeding, Stalled: q.Stalled,
				Paused: q.Paused, Errored: q.Errored, DownSpeed: q.DownSpeed, UpSpeed: q.UpSpeed,
			}
		}
	}

	if snap := a.deps.Attention.Current(); snap != nil {
		out.RequestsPending = snap.Counts.Requests
		out.NeedsYou = snap.Counts.Total
		out.Health.Errors = snap.Counts.HealthErrors
		out.Health.Warnings = snap.Counts.Health - snap.Counts.HealthErrors
	}
	// The health registry itself is the fresher word when it's there.
	if a.deps.Health != nil {
		out.Health.Errors, out.Health.Warnings = 0, 0
		for _, f := range a.deps.Health.Warnings() {
			if f.Level == health.LevelError {
				out.Health.Errors++
			} else {
				out.Health.Warnings++
			}
		}
	}
	out.Health.Problems = out.Health.Errors + out.Health.Warnings
	switch {
	case out.Health.Errors > 0:
		out.Health.Status = "error"
	case out.Health.Warnings > 0:
		out.Health.Status = "warning"
	default:
		out.Health.Status = "ok"
	}

	if st := a.deps.Updates.Status(ctx); st.Enabled {
		out.UpdateAvailable = st.Available
		if st.Latest != nil {
			out.LatestVersion = st.Latest.Tag
		}
	}
	return out
}

// widgetDisk reads the library disks off the request and waits up to widgetDiskWait for
// it. A statfs on a dead network mount can block for minutes and can't be cancelled, so a
// reading that's still running is never started again: the answer uses the last reading
// until it finishes, and at most one reading is ever stuck.
func (a *api) widgetDisk() widgetDisk {
	p := &a.widget.disk
	p.mu.Lock()
	done := p.running
	if done == nil {
		done = make(chan struct{})
		p.running = done
		measure := a.widget.volumes
		if measure == nil {
			measure = a.storageVolumes
		}
		safego.Go(a.deps.Log, "widget disk reading", func() {
			var d widgetDisk
			defer func() {
				p.mu.Lock()
				p.last = d // a reading that panicked leaves OK false
				p.running = nil
				p.mu.Unlock()
				close(done)
			}()
			d = diskTotals(measure(a.runCtx()))
		})
	}
	p.mu.Unlock()

	timer := time.NewTimer(widgetDiskWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// diskTotals adds up the volumes (fullest first, so the first is the tightest).
func diskTotals(vols []storageVolume) widgetDisk {
	d := widgetDisk{OK: len(vols) > 0}
	for i, v := range vols {
		d.FreeBytes += v.FreeBytes
		d.TotalBytes += v.TotalBytes
		if i == 0 {
			d.LowestFreeBytes = v.FreeBytes
			d.LowestFreePct = math.Round((100-v.UsedPct)*10) / 10
		}
	}
	return d
}
