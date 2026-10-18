package requests

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/notify"
	"github.com/tristenlammi/arrmada/internal/push"
	"github.com/tristenlammi/arrmada/internal/series"
)

// EventRequestCreated is the alert catalog's "New request" event.
const EventRequestCreated = "request.created"

// StaffAlerts is how a new request reaches staff, wired from main. Every field is
// optional; an unset one just skips that channel.
type StaffAlerts struct {
	// Emit queues a catalog alert for the admin's alert connections, exactly once per
	// dedupe key (notify.Service.EmitOnce).
	Emit func(ctx context.Context, key, dedupe string, data map[string]any) error
	// Staff lists the enabled managers and admins, who each get an inbox entry and a Web
	// Push.
	Staff func(ctx context.Context) ([]int64, error)
	// PushedByAlerts is who already gets the event as Web Push through an alert connection
	// ("This device"); they aren't pushed a second time here.
	PushedByAlerts func(ctx context.Context, key string) (map[int64]bool, error)
	// Name is how alerts name an account: its Plex or Jellyfin name when it has one
	// ("" = unknown; the account name, any email cut to its local part, is used then).
	Name func(ctx context.Context, userID int64) string
}

// SetStaffAlerts wires the "New request" alert, staff inbox and staff Web Push.
func (s *Service) SetStaffAlerts(a StaffAlerts) { s.staffAlerts = a }

// AlertName is how alerts and webhooks name a person: a display name (StaffAlerts.Name)
// when there is one, else the account name with an email cut to the part before the @.
// The account name itself, often an email address, never leaves the app this way; the
// staff pages inside it still show it.
func (s *Service) AlertName(ctx context.Context, userID int64, accountName string) string {
	if f := s.staffAlerts.Name; f != nil && userID > 0 {
		if n := notify.PersonName(f(ctx, userID)); n != "" {
			return n
		}
	}
	return notify.PersonName(accountName)
}

// alertStaff tells staff about a new ask waiting for their approval: the admin's alert
// connections (Apprise, "This device"), and each manager's and admin's inbox and Web Push
// (the push only for those who haven't turned off "Someone requests something").
// Called only from announceCreated, once per new pending row or re-opened declined one.
//
// The dedupe key and the inbox reference name the ask: the row's creation time, plus how
// many times it has been asked for again after a decline, so a re-request alerts again
// while a repeat of the same ask can never queue twice.
func (s *Service) alertStaff(ctx context.Context, req Request) {
	created := parseSQLiteTime(req.CreatedAt)
	if created.IsZero() {
		created = time.Now()
	}
	ask := fmt.Sprintf("%d:%d", req.ID, created.Unix())
	if req.ReRequest > 0 {
		ask += fmt.Sprintf(":%d", req.ReRequest)
	}
	data := map[string]any{
		"id": req.ID, "media_type": req.MediaType, "tmdb_id": req.TMDBID, "title": req.Title, "year": req.Year,
		"requested_by_name": s.AlertName(ctx, req.RequestedBy, req.RequestedByName), "note": req.Note,
		"rerequest": req.ReRequest > 0, "decline_reason": req.DeclineReason,
	}
	if req.MediaType == "series" && len(req.Seasons) > 0 {
		data["seasons"] = series.SeasonsLabel(req.Seasons)
	}
	s.AlertStaff(ctx, StaffNotice{
		Event: EventRequestCreated, Dedupe: EventRequestCreated + ":" + ask, Data: data,
		Ref: "request:" + strings.Replace(ask, ":", ":new:", 1), MediaType: req.MediaType,
		Except: req.RequestedBy, Pref: PrefNewRequest, What: "request", ID: req.ID,
	})
}

// StaffNotice is one thing staff are told about through AlertStaff: a catalog event (its
// Format writes the words) with the payload, the dedupe key it is queued under, and the
// inbox reference staff rows get.
type StaffNotice struct {
	Event, Dedupe string
	Data          map[string]any
	Ref           string
	MediaType     string
	// Except is who caused it (no one is told about their own ask); Pref is the choice
	// that silences the staff push ("" = always push).
	Except int64
	Pref   string
	// What and ID name it in the log ("request", 12).
	What string
	ID   int64
}

// AlertStaff tells staff about something waiting for them: the admin's alert connections
// (queued exactly once per dedupe key), and each manager's and admin's inbox and Web Push,
// skipping a push the admin's own "This device" connection already sends. New requests
// and reported problems both come this way.
func (s *Service) AlertStaff(ctx context.Context, n StaffNotice) {
	a := s.staffAlerts
	if a.Emit != nil {
		if err := a.Emit(ctx, n.Event, n.Dedupe, n.Data); err != nil {
			s.log.Warn(n.What+": couldn't queue the staff alert", n.What, n.ID, "err", err)
		}
	}
	if a.Staff == nil {
		return
	}
	def, ok := notify.Lookup(n.Event)
	if !ok {
		return
	}
	m, ok := def.Format(n.Data)
	if !ok {
		return
	}
	staff, err := a.Staff(ctx)
	if err != nil {
		s.log.Warn(n.What+": couldn't list staff for the notice", n.What, n.ID, "err", err)
		return
	}
	pushed := map[int64]bool{}
	if a.PushedByAlerts != nil {
		if p, err := a.PushedByAlerts(ctx, n.Event); err == nil {
			pushed = p
		}
	}
	now := time.Now().Unix()
	for _, uid := range staff {
		if uid <= 0 || uid == n.Except {
			continue // nobody is told about their own ask
		}
		inserted, err := s.repo.addUserNotification(ctx, uid, m.Title, m.Body, n.MediaType, n.Ref, now)
		if err != nil {
			s.log.Warn(n.What+": couldn't add the staff notice", n.What, n.ID, "user", uid, "err", err)
			continue
		}
		// Their choice silences the push; the inbox keeps the notice, as it does for every
		// other choice.
		if inserted && s.push != nil && !pushed[uid] && s.wantsPush(ctx, uid, n.Pref) {
			s.push.SendToUserAsync(uid, push.Message{Title: m.Title, Body: m.Body, URL: m.Link})
		}
	}
}
