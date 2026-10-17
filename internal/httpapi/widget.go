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
	"time"

	"github.com/tristenlammi/arrmada/internal/buildinfo"
	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/netutil"
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
//   - Each address may fetch it widgetPerMinute times a minute; a wrong key gets the same
//     401 whether or not a key has been made.

const (
	// Settings keys, both kept out of support bundles by their names (support.go): the
	// hash as a credential, the time as private.
	keyWidgetTokenHash    = "widget_token_hash"
	keyWidgetTokenCreated = "widget_key_created_at"
	// widgetTokenPrefix marks a widget key apart from an account API key ("arr_").
	widgetTokenPrefix = "arrw_"
	widgetTokenBytes  = 32
	// widgetPerMinute is how often one address may fetch the widget. Homepage refreshes
	// every 10 s by default for each open dashboard tab; this only stops a flood.
	widgetPerMinute = 120
	// widgetTimeout bounds one answer: the download client is the only live call.
	widgetTimeout = 5 * time.Second
)

func hashWidgetToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// widgetKeyState is GET /system/widget-key: whether a key exists and since when — never
// the key, which isn't stored.
type widgetKeyState struct {
	Enabled   bool  `json:"enabled"`
	CreatedAt int64 `json:"created_at,omitempty"`
}

func (a *api) widgetKeyState(ctx context.Context) widgetKeyState {
	if a.deps.Settings == nil || a.deps.Settings.Get(ctx, keyWidgetTokenHash, "") == "" {
		return widgetKeyState{}
	}
	at, _ := strconv.ParseInt(a.deps.Settings.Get(ctx, keyWidgetTokenCreated, ""), 10, 64)
	return widgetKeyState{Enabled: true, CreatedAt: at}
}

// widgetKeyValid reports whether key is the install's widget key.
func (a *api) widgetKeyValid(ctx context.Context, key string) bool {
	if a.deps.Settings == nil || !strings.HasPrefix(key, widgetTokenPrefix) || len(key) > 128 {
		return false
	}
	want := a.deps.Settings.Get(ctx, keyWidgetTokenHash, "")
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashWidgetToken(key)), []byte(want)) == 1
}

// handleWidgetKey is GET /api/v1/system/widget-key (admin).
func (a *api) handleWidgetKey(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.widgetKeyState(r.Context()))
}

// handleRotateWidgetKey makes a new widget key, replacing any earlier one, and answers with
// it — the only time it's shown — and the widget's address as this request reached it.
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
	ctx := r.Context()
	// The time first: a failure between the two writes leaves the old key working, never a
	// half-made new one.
	if err := a.deps.Settings.Set(ctx, keyWidgetTokenCreated, strconv.FormatInt(now, 10)); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the widget key")
		return
	}
	if err := a.deps.Settings.Set(ctx, keyWidgetTokenHash, hashWidgetToken(key)); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the widget key")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "created_at": now, "key": key,
		"url": baseURL(r) + "/api/v1/widget/homepage",
	})
}

// handleRevokeWidgetKey turns the widget key off; dashboards using it get 401.
func (a *api) handleRevokeWidgetKey(w http.ResponseWriter, r *http.Request) {
	if a.deps.Settings == nil {
		a.writeError(w, http.StatusInternalServerError, "settings unavailable")
		return
	}
	ctx := r.Context()
	if err := a.deps.Settings.Set(ctx, keyWidgetTokenHash, ""); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not turn the widget key off")
		return
	}
	_ = a.deps.Settings.Set(ctx, keyWidgetTokenCreated, "")
	a.writeJSON(w, http.StatusOK, widgetKeyState{})
}

// homepageWidget is GET /api/v1/widget/homepage. Every field is a number, a yes/no or a
// fixed word: the privacy test walks the answer and fails on any other string.
type homepageWidget struct {
	Movies   int `json:"movies"`
	Shows    int `json:"shows"`
	Episodes int `json:"episodes"`
	Books    int `json:"books"`
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
// up; LowestFree* is the fullest one, the one about to cause trouble.
type widgetDisk struct {
	FreeBytes       uint64  `json:"free_bytes"`
	TotalBytes      uint64  `json:"total_bytes"`
	LowestFreeBytes uint64  `json:"lowest_free_bytes"`
	LowestFreePct   float64 `json:"lowest_free_pct"`
}

// handleHomepageWidget is GET /api/v1/widget/homepage, opened only by the widget key.
func (a *api) handleHomepageWidget(w http.ResponseWriter, r *http.Request) {
	if l := a.widgetLimiter; l != nil {
		if ok, retry := l.allow("widget:" + netutil.ClientIP(r)); !ok {
			a.tooManyAttempts(w, retry)
			return
		}
	}
	if !a.widgetKeyValid(r.Context(), apiKeyFromRequest(r)) {
		a.writeError(w, http.StatusUnauthorized, "a valid widget key is required (Settings → System → Dashboard widget)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), widgetTimeout)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, http.StatusOK, a.homepageWidget(ctx))
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
	}

	if a.deps.Downloads != nil {
		if items, err := a.deps.Downloads.Queue(ctx); err == nil {
			q := summarizeQueue(items)
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

	// Fullest first, so the first volume is the tightest.
	for i, v := range a.storageVolumes(ctx) {
		out.Disk.FreeBytes += v.FreeBytes
		out.Disk.TotalBytes += v.TotalBytes
		if i == 0 {
			out.Disk.LowestFreeBytes = v.FreeBytes
			out.Disk.LowestFreePct = math.Round((100-v.UsedPct)*10) / 10
		}
	}

	if st := a.deps.Updates.Status(ctx); st.Enabled {
		out.UpdateAvailable = st.Available
		if st.Latest != nil {
			out.LatestVersion = st.Latest.Tag
		}
	}
	return out
}
