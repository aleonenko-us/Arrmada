package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/notify"
)

// Admin alert connections. A saved Apprise URL never comes back out: the list carries a
// hint (scheme and, where it isn't secret, the host), an edit that leaves the URL out
// keeps the stored one, and a saved connection is tested by id. Staff can see the list;
// only admins change it or make it send. Preset connections (Discord, Telegram, ntfy,
// Pushover, email) are saved from form fields the server builds the URL from
// (notify/presets.go); their secret fields are masked the same way.

// notificationView is a connection as the API shows it: everything but the URL and the
// secrets it was built from.
type notificationView struct {
	notify.Connection
	// Config shadows the connection's own: only a push connection's {"user_id"} goes out.
	// A preset's config holds its fields, secrets included; those go out as Fields.
	Config  json.RawMessage `json:"config"`
	URLHint string          `json:"url_hint"`
	URLSet  bool            `json:"url_set"`
	// Preset is the form the connection is edited with: discord, telegram, ntfy,
	// pushover, email, webpush, or custom (a raw Apprise URL — every connection made
	// before presets). Fields are its form values, each secret blank with
	// "<name>_set": true beside it.
	Preset string         `json:"preset"`
	Fields map[string]any `json:"fields,omitempty"`
	// InvalidReason says why a URL saved before validation existed no longer passes it.
	// The connection still sends; editing it needs a valid URL.
	InvalidReason string `json:"invalid_reason,omitempty"`
	// The latest delivery outcome, for the card's status dot.
	notify.DeliveryState
}

func viewOf(c notify.Connection) notificationView {
	v := notificationView{Connection: c, Config: json.RawMessage("{}"), URLHint: notify.URLHint(c.URL), URLSet: c.URL != "", Preset: notify.PresetCustom}
	switch fields := notify.PresetFields(c); {
	case c.Kind == notify.KindWebPush:
		v.Preset = notify.KindWebPush
		v.Config = notify.PushConfigFor(c.PushUserID())
	case fields != nil:
		v.Preset = c.Kind
		v.Fields = notify.MaskedFields(c.Kind, fields)
	}
	if c.URL != "" && c.Kind != notify.KindWebPush {
		if err := notify.ValidateAppriseURL(c.URL); err != nil {
			v.InvalidReason = err.Error()
		}
	}
	return v
}

// notificationInput is what create and update accept. URL is a pointer so an update can
// leave it out (or send "") to keep the stored one; Events likewise (a new connection
// left without them starts on the catalog's defaults).
type notificationInput struct {
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	URL     *string   `json:"url"`
	Events  *[]string `json:"events"`
	Enabled bool      `json:"enabled"`
	// Config is accepted so a client may echo a connection back, but never used: a push
	// connection's target is always the signed-in user (pushTarget).
	Config json.RawMessage `json:"config"`
	// Fields are a preset's form values (kind discord, telegram, ntfy, pushover, email);
	// the server builds the URL from them. A secret left blank keeps the stored one.
	Fields map[string]string `json:"fields"`
	// ID, on a Test of an unsaved edit, names the saved connection whose stored secrets
	// fill the fields left blank.
	ID int64 `json:"id"`
	// Options are the message choices (Plex stream filters); left out keeps the stored ones.
	Options *notify.Options `json:"options"`
}

// presetURL builds a preset connection's URL from the form, filling blank secrets from
// stored (the same kind's saved fields; nil for a new one). It returns the fields to keep.
func presetURL(kind string, in, stored map[string]string) (string, map[string]string, error) {
	fields := notify.MergeSecrets(kind, in, stored)
	u, err := notify.BuildURL(kind, fields)
	if err != nil {
		return "", nil, err
	}
	return u, fields, nil
}

// usesFields says whether a request sets a connection up from a preset's fields rather
// than a raw URL. A client that only sends a URL (an older page, an API script) keeps
// working as before: the connection becomes a custom one.
func (in notificationInput) usesFields() bool {
	return in.Fields != nil && notify.IsFieldPreset(in.Kind)
}

func (in notificationInput) url() string {
	if in.URL == nil {
		return ""
	}
	return strings.TrimSpace(*in.URL)
}

func (in notificationInput) apply(c *notify.Connection) {
	c.Name, c.Kind, c.Enabled = strings.TrimSpace(in.Name), in.Kind, in.Enabled
	if in.Events != nil {
		c.Events = append([]string{}, *in.Events...)
	}
	if in.Options != nil {
		c.Options = *in.Options
	}
}

// checkEvents refuses event keys the catalog doesn't know, and options that don't pass
// their check (which it tidies in place).
func (a *api) checkEvents(w http.ResponseWriter, in *notificationInput) bool {
	if in.Events != nil {
		if err := notify.ValidEvents(*in.Events); err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return false
		}
	}
	if in.Options != nil {
		o, err := in.Options.Clean()
		if err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return false
		}
		in.Options = &o
	}
	return true
}

// handleNotificationCatalog lists the events a connection can subscribe to, grouped
// for the Alerts page, with the default message template and the placeholders a
// connection's own template may use. links says whether alerts can link back to the app
// (an outside address is set), so the page can say why {link} would be blank.
func (a *api) handleNotificationCatalog(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{
		"groups": notify.Groups(), "events": notify.Catalog(),
		"template": notify.DefaultTemplate, "placeholders": notify.Placeholders,
		"links": a.deps.Settings != nil && a.deps.Settings.PublicURL(r.Context()) != "",
	})
}

// handleNotificationPresets lists the "Where should alerts go?" tiles and each one's form.
func (a *api) handleNotificationPresets(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{"presets": notify.Presets})
}

// handleListNotifications returns all notification connections, URLs redacted.
func (a *api) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Notify.List(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list notifications")
		return
	}
	states, err := a.deps.Notify.DeliveryStates(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list notifications")
		return
	}
	out := make([]notificationView, 0, len(list))
	for _, c := range list {
		v := viewOf(c)
		v.DeliveryState = states[c.ID]
		out = append(out, v)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}

// handleNotificationDeliveries is a connection's recent delivery log (newest first).
func (a *api) handleNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.deps.Notify.Deliveries(r.Context(), id, limit)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load deliveries")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"deliveries": list})
}

func (a *api) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var in notificationInput
	if !a.decodeJSON(w, r, &in) {
		return
	}
	if !a.checkEvents(w, &in) {
		return
	}
	c := notify.Connection{Events: notify.DefaultEvents()}
	in.apply(&c)
	c.URL = in.url()
	if c.Kind == notify.KindWebPush {
		// "This device": pushes to the signed-in admin's own subscribed devices. The
		// target is always the session's user, whatever the body says, so nobody can
		// point alerts at someone else's phone.
		if !a.pushTarget(w, r, &c) {
			return
		}
		created, err := a.deps.Notify.Create(r.Context(), c)
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not create notification")
			return
		}
		a.writeJSON(w, http.StatusCreated, viewOf(created))
		return
	}
	if in.usesFields() {
		if c.Name == "" {
			a.writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		u, fields, err := presetURL(c.Kind, in.Fields, nil)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		c.URL, c.Config = u, notify.PresetConfigFor(c.Kind, fields)
	} else {
		if c.Name == "" || c.URL == "" {
			a.writeError(w, http.StatusBadRequest, "name and url are required")
			return
		}
		// Checked on save, not at send time: a typo or an option-looking string would
		// otherwise be stored and only fail when an alert is due.
		if err := notify.ValidateAppriseURL(c.URL); err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		c.Config = nil // a raw URL is a custom connection; nothing else is kept
	}
	created, err := a.deps.Notify.Create(r.Context(), c)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not create notification")
		return
	}
	a.writeJSON(w, http.StatusCreated, viewOf(created))
}

func (a *api) handleUpdateNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var in notificationInput
	if !a.decodeJSON(w, r, &in) {
		return
	}
	c, err := a.deps.Notify.Get(r.Context(), id)
	if errors.Is(err, notify.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "notification not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load notification")
		return
	}
	if !a.checkEvents(w, &in) {
		return
	}
	stored := c
	in.apply(&c)
	if c.Name == "" {
		a.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// A connection's kind is fixed: a push connection keeps its user and takes no URL,
	// and an Apprise one can't become a push connection by renaming its kind.
	if stored.Kind == notify.KindWebPush || c.Kind == notify.KindWebPush {
		if stored.Kind != c.Kind {
			a.writeError(w, http.StatusBadRequest, "a push connection can't become a link connection, or the other way round — add a new one")
			return
		}
		if in.url() != "" {
			a.writeError(w, http.StatusBadRequest, "a push connection doesn't take a link")
			return
		}
		c.Config = stored.Config
		if err := a.deps.Notify.Update(r.Context(), id, c); err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not update notification")
			return
		}
		a.writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
		return
	}
	switch u := in.url(); {
	case in.usesFields():
		// A preset's form: a secret left blank keeps the stored one — but only from the
		// same kind of preset, so switching Discord to Telegram can't carry a token over.
		var kept map[string]string
		if c.Kind == stored.Kind {
			kept = notify.PresetFields(stored)
		}
		built, fields, err := presetURL(c.Kind, in.Fields, kept)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		c.URL, c.Config = built, notify.PresetConfigFor(c.Kind, fields)
	case u != "" && u != notify.URLHint(stored.URL):
		// A new raw URL must pass validation; the connection is a custom one from now on.
		if err := notify.ValidateAppriseURL(u); err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		c.URL, c.Config = u, json.RawMessage("{}")
	default:
		// No URL, a blank one or the masked hint sent back keeps the stored URL, so
		// renaming or ticking events doesn't mean retyping a token. A preset whose kind
		// changed without new fields keeps its URL as a custom connection.
		if c.Kind != stored.Kind {
			c.Config = json.RawMessage("{}")
		}
	}
	if err := a.deps.Notify.Update(r.Context(), id, c); err != nil {
		if errors.Is(err, notify.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "notification not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "could not update notification")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

func (a *api) handleDeleteNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Notify.Delete(r.Context(), id); err != nil {
		if errors.Is(err, notify.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "notification not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "could not delete notification")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestNotification sends a sample message to an unsaved connection in the body,
// so a URL can be tried before it's saved.
func (a *api) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	var in notificationInput
	if !a.decodeJSON(w, r, &in) {
		return
	}
	var c notify.Connection
	in.apply(&c)
	c.URL = in.url()
	if c.Kind == notify.KindWebPush {
		c.Name = "This device"
		if !a.pushTarget(w, r, &c) {
			return
		}
		a.answerTest(w, r, c)
		return
	}
	if in.usesFields() {
		// An edit being tried before it's saved: blank secrets come from the saved
		// connection of the same kind, server-side, so the browser never needs them.
		var kept map[string]string
		if in.ID > 0 {
			if saved, err := a.deps.Notify.Get(r.Context(), in.ID); err == nil && saved.Kind == c.Kind {
				kept = notify.PresetFields(saved)
			}
		}
		u, _, err := presetURL(c.Kind, in.Fields, kept)
		if err != nil {
			a.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		c.URL = u
	}
	if err := notify.ValidateAppriseURL(c.URL); err != nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.answerTest(w, r, c)
}

// pushTarget makes c a push connection for the signed-in user, refusing a URL.
func (a *api) pushTarget(w http.ResponseWriter, r *http.Request, c *notify.Connection) bool {
	u, ok := userFrom(r)
	if !ok || u == nil || u.ID <= 0 {
		a.writeError(w, http.StatusBadRequest, "push alerts need a signed-in account")
		return false
	}
	if c.URL != "" {
		a.writeError(w, http.StatusBadRequest, "a push connection doesn't take a link")
		return false
	}
	if c.Name == "" {
		a.writeError(w, http.StatusBadRequest, "name is required")
		return false
	}
	c.Config = notify.PushConfigFor(u.ID)
	return true
}

// handleTestSavedNotification sends a sample message through a saved connection's
// stored URL, which the browser never sees.
func (a *api) handleTestSavedNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	c, err := a.deps.Notify.Get(r.Context(), id)
	if errors.Is(err, notify.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "notification not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load notification")
		return
	}
	a.answerTest(w, r, c)
}

func (a *api) answerTest(w http.ResponseWriter, r *http.Request, c notify.Connection) {
	if err := a.deps.Notify.Test(r.Context(), c); err != nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
