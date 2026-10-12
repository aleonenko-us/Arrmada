package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/mail"
)

// Outgoing email (Settings → Alerts → Outgoing email): the mail account Arrmada sends
// from, for Send to Kindle. The server, the login and the password are credentials: they
// live in the API key store, are typed in this card only, and only ever come back to the
// browser as "set" plus a masked hint. Port, security and the From address are ordinary
// settings (the From address is shown to everyone who sends to a Kindle, since Amazon has
// to be told to trust it). Logs carry the outcome, never the server, login or recipient.

const (
	keySMTPPort     = "smtp_port"
	keySMTPSecurity = "smtp_security"
	keySMTPFrom     = "smtp_from"
)

// mailConfig is the saved sending setup and its password, read fresh on every use.
func (a *api) mailConfig(ctx context.Context) (mail.Config, string) {
	cfg := mail.Config{
		Security: a.deps.Settings.Get(ctx, keySMTPSecurity, mail.SecurityStartTLS),
		From:     a.deps.Settings.Get(ctx, keySMTPFrom, ""),
	}
	if !mail.ValidSecurity(cfg.Security) {
		cfg.Security = mail.SecurityStartTLS
	}
	cfg.Port, _ = strconv.Atoi(a.deps.Settings.Get(ctx, keySMTPPort, ""))
	if cfg.Port <= 0 {
		cfg.Port = mail.DefaultPort(cfg.Security)
	}
	var password string
	if k := a.deps.APIKeys; k != nil {
		cfg.Host = k.Value(ctx, apikeys.SMTPHost)
		cfg.Username = k.Value(ctx, apikeys.SMTPUsername)
		password = k.Value(ctx, apikeys.SMTPPassword)
	}
	return cfg, password
}

// mailReady reports whether a send can be tried at all.
func (a *api) mailReady(ctx context.Context) bool {
	cfg, _ := a.mailConfig(ctx)
	return cfg.Ready()
}

// smtpView is the browser's view of the setup: never a credential in full.
type smtpView struct {
	Configured   bool   `json:"configured"` // a server and a From address are saved
	HostSet      bool   `json:"host_set"`
	HostHint     string `json:"host_hint,omitempty"`
	Port         int    `json:"port"`
	Security     string `json:"security"`
	From         string `json:"from"`
	UsernameSet  bool   `json:"username_set"`
	UsernameHint string `json:"username_hint,omitempty"`
	PasswordSet  bool   `json:"password_set"`
}

func (a *api) smtpView(ctx context.Context) smtpView {
	cfg, password := a.mailConfig(ctx)
	return smtpView{
		Configured: cfg.Ready(), HostSet: cfg.Host != "", HostHint: maskHint(cfg.Host),
		Port: cfg.Port, Security: cfg.Security, From: cfg.From,
		UsernameSet: cfg.Username != "", UsernameHint: maskHint(cfg.Username),
		PasswordSet: password != "",
	}
}

// maskHint shows enough of a saved value to recognise it ("sm….com") and no more.
func maskHint(v string) string {
	r := []rune(v)
	switch {
	case len(r) == 0:
		return ""
	case len(r) <= 8:
		return "…"
	}
	return string(r[:2]) + "…" + string(r[len(r)-4:])
}

// handleGetSMTP returns the outgoing-email setup.
//
//	GET /api/v1/settings/smtp
func (a *api) handleGetSMTP(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.smtpView(r.Context()))
}

// smtpUpdate is a PUT body. The credentials keep their saved value when left blank, the
// way the API keys do: the card never has them to send back.
type smtpUpdate struct {
	Host        string  `json:"host"`
	Port        *int    `json:"port"`
	Security    *string `json:"security"`
	Username    string  `json:"username"`
	Password    string  `json:"password"`
	From        *string `json:"from"`
	RemoveLogin bool    `json:"remove_login"` // a relay that takes mail without signing in
}

// handleSetSMTP saves the outgoing-email setup.
//
//	PUT /api/v1/settings/smtp
func (a *api) handleSetSMTP(w http.ResponseWriter, r *http.Request) {
	var req smtpUpdate
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if a.deps.APIKeys == nil {
		a.writeError(w, http.StatusInternalServerError, "key store unavailable")
		return
	}
	ctx := r.Context()
	host := strings.TrimSpace(req.Host)
	user := strings.TrimSpace(req.Username)
	// Check everything before saving anything, so a bad field leaves the setup as it was.
	if host != "" {
		if err := mail.ValidHost(host); err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Port != nil && (*req.Port < 0 || *req.Port > 65535) {
		a.writeError(w, http.StatusBadRequest, "the port must be between 1 and 65535")
		return
	}
	if req.Security != nil && !mail.ValidSecurity(*req.Security) {
		a.writeError(w, http.StatusBadRequest, "security must be STARTTLS, TLS or None")
		return
	}
	from := ""
	if req.From != nil {
		from = strings.TrimSpace(*req.From)
		if from != "" {
			if _, err := mail.ParseAddress(from); err != nil {
				a.writeError(w, http.StatusBadRequest, "the From address isn't valid: "+err.Error())
				return
			}
		}
	}
	if len(user) > 320 || len(req.Password) > 1024 || strings.ContainsAny(user+req.Password, "\r\n\x00") {
		a.writeError(w, http.StatusBadRequest, "the username or password isn't valid")
		return
	}
	if req.RemoveLogin && (user != "" || req.Password != "") {
		a.writeError(w, http.StatusBadRequest, "remove the login or change it, not both")
		return
	}

	keys := a.deps.APIKeys
	var saveErr error
	set := func(err error) {
		if err != nil && saveErr == nil {
			saveErr = err
		}
	}
	if host != "" {
		set(keys.Set(ctx, apikeys.SMTPHost, host))
	}
	if req.RemoveLogin {
		set(keys.Clear(ctx, apikeys.SMTPUsername))
		set(keys.Clear(ctx, apikeys.SMTPPassword))
	}
	if user != "" {
		set(keys.Set(ctx, apikeys.SMTPUsername, user))
	}
	if req.Password != "" {
		set(keys.Set(ctx, apikeys.SMTPPassword, req.Password))
	}
	if req.Security != nil {
		set(a.deps.Settings.Set(ctx, keySMTPSecurity, *req.Security))
	}
	if req.Port != nil {
		port := ""
		if *req.Port > 0 {
			port = strconv.Itoa(*req.Port)
		}
		set(a.deps.Settings.Set(ctx, keySMTPPort, port))
	}
	if req.From != nil {
		set(a.deps.Settings.Set(ctx, keySMTPFrom, from))
	}
	if saveErr != nil {
		a.deps.Log.Warn("outgoing email: saving the settings failed")
		a.writeError(w, http.StatusInternalServerError, "could not save the outgoing email settings")
		return
	}
	a.deps.Log.Info("outgoing email settings saved")
	a.writeJSON(w, http.StatusOK, a.smtpView(ctx))
}

// handleClearSMTP forgets the whole setup: server, login, password and the rest.
//
//	DELETE /api/v1/settings/smtp
func (a *api) handleClearSMTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var errs []error
	if k := a.deps.APIKeys; k != nil {
		for _, id := range []string{apikeys.SMTPHost, apikeys.SMTPUsername, apikeys.SMTPPassword} {
			errs = append(errs, k.Clear(ctx, id))
		}
	}
	for _, key := range []string{keySMTPPort, keySMTPSecurity, keySMTPFrom} {
		errs = append(errs, a.deps.Settings.Set(ctx, key, ""))
	}
	if errors.Join(errs...) != nil {
		a.writeError(w, http.StatusInternalServerError, "could not remove the outgoing email settings")
		return
	}
	a.deps.Log.Info("outgoing email settings removed")
	a.writeJSON(w, http.StatusOK, a.smtpView(ctx))
}

// smtpTestTimeout bounds the Test button's send: long enough for a slow provider, short
// enough that the card doesn't hang on a server that never answers.
const smtpTestTimeout = 45 * time.Second

// handleTestSMTP sends "Arrmada test email" to an address with the saved setup and says
// what happened; a refusal carries the mail server's own reply ("535 5.7.8 Username and
// Password not accepted"), not a generic error.
//
//	POST /api/v1/settings/smtp/test {to}
func (a *api) handleTestSMTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		To string `json:"to"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if _, err := mail.ParseAddress(req.To); err != nil {
		a.writeError(w, http.StatusBadRequest, "send the test to: "+err.Error())
		return
	}
	cfg, password := a.mailConfig(r.Context())
	if !cfg.Ready() {
		a.writeError(w, http.StatusConflict, "save a mail server and a From address first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), smtpTestTimeout)
	defer cancel()
	err := mail.Send(ctx, cfg, password, mail.Message{
		To:      strings.TrimSpace(req.To),
		Subject: "Arrmada test email",
		Body:    "This is a test from Arrmada's outgoing email settings. If you can read it, sending works.",
	})
	if err != nil {
		a.deps.Log.Warn("outgoing email: test failed", "code", mail.ReplyCode(err))
		a.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	a.deps.Log.Info("outgoing email: test sent")
	a.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": "Sent. Check that inbox (and its spam folder)."})
}
