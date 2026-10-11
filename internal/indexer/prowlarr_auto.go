package indexer

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/connstatus"
)

// Settings keys the Prowlarr connection lives in. The URL and key are saved by the first
// sync that works; the UI address is optional and only used for the "Open Prowlarr" link.
const (
	SettingProwlarrURL      = "prowlarr_url"
	SettingProwlarrKey      = "prowlarr_api_key"
	SettingProwlarrLastSync = "prowlarr_last_sync"
	SettingProwlarrUIURL    = "prowlarr_ui_url"
)

// ProwlarrStatusRef is the Prowlarr connection's ref in the integration status tracker
// (kind connstatus.KindProwlarr): there is only ever one.
const ProwlarrStatusRef = "default"

// Triggers a recorded sync can have.
const (
	ProwlarrTriggerSchedule = "schedule"
	ProwlarrTriggerManual   = "manual"
)

// ProwlarrLastSync is the outcome of the latest Prowlarr sync, scheduled or clicked, as
// the Prowlarr panel and the health check read it. Error is redacted and never carries
// the key.
type ProwlarrLastSync struct {
	At        time.Time `json:"at"`
	Trigger   string    `json:"trigger"`
	Added     int       `json:"added"`
	Updated   int       `json:"updated"`
	Disabled  int       `json:"disabled"`
	Reenabled int       `json:"reenabled"`
	Error     string    `json:"error,omitempty"`
	// FailedRuns is how many syncs in a row have failed (0 after a success), so one
	// blip isn't a health warning but a Prowlarr that stays down is.
	FailedRuns int `json:"failed_runs,omitempty"`
}

// ProwlarrSettings is the shared settings service, as far as the Prowlarr sync needs it.
type ProwlarrSettings interface {
	Get(ctx context.Context, key, def string) string
	Set(ctx context.Context, key, value string) error
}

// ReadProwlarrLastSync returns the saved outcome of the latest sync; ok is false before
// the first one (or if what's saved can't be read).
func ReadProwlarrLastSync(ctx context.Context, st ProwlarrSettings) (ProwlarrLastSync, bool) {
	raw := st.Get(ctx, SettingProwlarrLastSync, "")
	if raw == "" {
		return ProwlarrLastSync{}, false
	}
	var ls ProwlarrLastSync
	if json.Unmarshal([]byte(raw), &ls) != nil || ls.At.IsZero() {
		return ProwlarrLastSync{}, false
	}
	return ls, true
}

// RecordProwlarrSync saves how a sync went and records it as the Prowlarr connection's
// status. A person's sync that fails (a mistyped key in the form, say) is recorded too:
// the panel then says why, and the next scheduled run with the saved key replaces it.
func (s *Service) RecordProwlarrSync(ctx context.Context, st ProwlarrSettings, trigger string, res SyncResult, err error) ProwlarrLastSync {
	s.prowlarrMu.Lock()
	defer s.prowlarrMu.Unlock()
	prev, _ := ReadProwlarrLastSync(ctx, st)
	ls := ProwlarrLastSync{
		At: time.Now().UTC(), Trigger: trigger,
		Added: res.Added, Updated: res.Updated, Disabled: res.Disabled, Reenabled: res.Reenabled,
	}
	if err != nil {
		// Only the scheduled runs, with the saved URL and key, count towards "keeps
		// failing": a person trying a mistyped key in the form says nothing about Prowlarr.
		failed := prev.FailedRuns
		if trigger == ProwlarrTriggerSchedule {
			failed++
		}
		ls = ProwlarrLastSync{At: ls.At, Trigger: trigger, Error: connstatus.Redact(err.Error()), FailedRuns: failed}
	}
	if b, mErr := json.Marshal(ls); mErr == nil {
		if sErr := st.Set(ctx, SettingProwlarrLastSync, string(b)); sErr != nil {
			s.log.Warn("prowlarr: couldn't save the sync outcome", "err", sErr)
		}
	}
	// No backoff: the sync runs hourly, and a person's Sync now must always be tried.
	s.status.Record(connstatus.KindProwlarr, ProwlarrStatusRef, connstatus.Outcome{Err: err, Name: "Prowlarr"})
	return ls
}

// ScheduledProwlarrSync is the hourly "prowlarr-sync" task: the same non-destructive sync
// as the Sync now button, with the saved URL and key. Until a sync has worked once (which
// is what saves them) it does nothing, so an install without Prowlarr never errors.
// flareURL is Arrmada's FlareSolverr, for the bundled Prowlarr's proxy. Rows the sync adds
// get their capabilities read straight after, as a manual sync does in the background.
func (s *Service) ScheduledProwlarrSync(ctx context.Context, st ProwlarrSettings, flareURL func(context.Context) string) error {
	base := strings.TrimSpace(st.Get(ctx, SettingProwlarrURL, ""))
	key := st.Get(ctx, SettingProwlarrKey, "")
	if base == "" || key == "" {
		return nil
	}
	flare := ""
	if flareURL != nil {
		flare = flareURL(ctx)
	}
	res, err := s.SyncProwlarr(ctx, ProwlarrSync{URL: base, APIKey: key, FlareSolverrURL: flare})
	if err != nil && ctx.Err() != nil {
		return err // shutting down: not Prowlarr's failure, so nothing is recorded
	}
	s.RecordProwlarrSync(ctx, st, ProwlarrTriggerSchedule, res, err)
	if err != nil {
		return err
	}
	if res.Added+res.Disabled+res.Reenabled+res.Updated > 0 {
		s.log.Info("prowlarr: scheduled sync changed indexers",
			"added", res.Added, "updated", res.Updated, "disabled", res.Disabled, "reenabled", res.Reenabled)
	}
	if len(res.AddedIDs) > 0 {
		if cErr := s.RefreshCaps(ctx, res.AddedIDs); cErr != nil && ctx.Err() == nil {
			s.log.Warn("prowlarr: couldn't read new indexers' capabilities", "err", cErr)
		}
	}
	return nil
}
