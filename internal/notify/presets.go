package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Presets are the alert destinations an admin can set up from a short form instead of
// writing an Apprise URL: paste a Discord webhook, type a Telegram bot token and chat id,
// and so on. The server builds the Apprise URL from the fields (BuildURL), checks it like
// any other, and keeps both — the fields in the connection's config so the form can show
// them again, the URL for sending. Secret fields never go back out: a list shows them as
// "<name>_set": true, and saving one blank keeps what's stored.
//
// "custom" is the raw Apprise URL for everything else (and every connection made before
// presets existed); "webpush" is "This device", which needs no fields at all.

// Preset kinds.
const (
	PresetDiscord  = "discord"
	PresetTelegram = "telegram"
	PresetNtfy     = "ntfy"
	PresetPushover = "pushover"
	PresetEmail    = "email"
	PresetCustom   = "custom"
)

// FieldDef is one input on a preset's form.
type FieldDef struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Hint        string   `json:"hint,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"` // a choice: the first is the default
}

// PresetDef is one tile under "Where should alerts go?".
type PresetDef struct {
	Kind   string     `json:"kind"`
	Label  string     `json:"label"`
	Hint   string     `json:"hint"` // where to find what the form asks for
	Fields []FieldDef `json:"fields"`
}

// Presets lists the tiles in the order the page shows them.
var Presets = []PresetDef{
	{
		Kind: PresetDiscord, Label: "Discord",
		Hint: "Discord: Server Settings → Integrations → Webhooks → New Webhook → Copy Webhook URL.",
		Fields: []FieldDef{
			{Name: "webhook_url", Label: "Webhook URL", Placeholder: "https://discord.com/api/webhooks/…", Secret: true, Required: true},
		},
	},
	{
		Kind: PresetTelegram, Label: "Telegram",
		Hint: "Telegram: message @BotFather, send /newbot and copy the token; then message your bot and get the chat id from @userinfobot (a group's id starts with -).",
		Fields: []FieldDef{
			{Name: "bot_token", Label: "Bot token", Placeholder: "123456789:AA…", Secret: true, Required: true},
			{Name: "chat_id", Label: "Chat id", Placeholder: "123456789, -100…, or @channel", Required: true},
		},
	},
	{
		Kind: PresetNtfy, Label: "ntfy",
		Hint: "ntfy: pick a topic name that's hard to guess and subscribe to it in the ntfy app. Leave the server blank for ntfy.sh.",
		Fields: []FieldDef{
			{Name: "server", Label: "Server", Placeholder: "https://ntfy.sh", Hint: "Blank = ntfy.sh"},
			// On a public server the topic name is the only thing keeping others out.
			{Name: "topic", Label: "Topic", Placeholder: "arrmada-7f3k2q", Secret: true, Required: true},
			{Name: "token", Label: "Access token", Placeholder: "tk_… (only if the topic is protected)", Secret: true},
		},
	},
	{
		Kind: PresetPushover, Label: "Pushover",
		Hint: "Pushover: your user key is on the pushover.net dashboard; create an application there for the API token.",
		Fields: []FieldDef{
			{Name: "user_key", Label: "User key", Secret: true, Required: true},
			{Name: "app_token", Label: "Application API token", Secret: true, Required: true},
		},
	},
	{
		Kind: PresetEmail, Label: "Email",
		Hint: "Email: your mail provider's SMTP server. For Gmail use smtp.gmail.com, port 587, and an app password.",
		Fields: []FieldDef{
			{Name: "host", Label: "SMTP server", Placeholder: "smtp.gmail.com", Required: true},
			{Name: "port", Label: "Port", Placeholder: "587"},
			{Name: "security", Label: "Security", Options: []string{"starttls", "ssl", "none"}},
			{Name: "username", Label: "Username", Placeholder: "you@example.com"},
			{Name: "password", Label: "Password", Secret: true},
			{Name: "from", Label: "From", Placeholder: "you@example.com", Required: true},
			{Name: "to", Label: "To", Placeholder: "you@example.com (several: comma-separated)", Required: true},
		},
	},
	{
		Kind: PresetCustom, Label: "Custom (Apprise URL)",
		Hint: "Any of Apprise's 80+ services as one link, e.g. gotify://, matrix://, slack://.",
		Fields: []FieldDef{
			{Name: "url", Label: "Apprise URL", Placeholder: "gotify://host/token", Secret: true, Required: true},
		},
	},
	{
		Kind: KindWebPush, Label: "This device",
		Hint:   "Web Push to this browser or phone — nothing else to set up.",
		Fields: []FieldDef{},
	},
}

// PresetFor returns a preset by kind.
func PresetFor(kind string) (PresetDef, bool) {
	for _, p := range Presets {
		if p.Kind == kind {
			return p, true
		}
	}
	return PresetDef{}, false
}

// IsFieldPreset reports whether kind is built from form fields (not the raw URL of
// "custom", nor "This device").
func IsFieldPreset(kind string) bool {
	p, ok := PresetFor(kind)
	return ok && kind != PresetCustom && kind != KindWebPush && len(p.Fields) > 0
}

var (
	reDiscordID    = regexp.MustCompile(`^[0-9]{5,25}$`)
	reToken        = regexp.MustCompile(`^[A-Za-z0-9_-]{8,256}$`)
	reTelegramBot  = regexp.MustCompile(`^[0-9]{3,15}:[A-Za-z0-9_-]{20,80}$`)
	reTelegramChat = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z][A-Za-z0-9_]{3,64})$`)
	reNtfyTopic    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	rePushoverKey  = regexp.MustCompile(`^[A-Za-z0-9]{20,40}$`)
	reHost         = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

// BuildURL turns a preset's form fields into its Apprise URL, checked like any saved URL
// (ValidateAppriseURL). Every part that came from the form is escaped, so a password with
// "@" or "/" in it can't change where the URL points. Errors name the field, never its
// value.
func BuildURL(kind string, f map[string]string) (string, error) {
	p, ok := PresetFor(kind)
	if !ok || kind == KindWebPush {
		return "", fmt.Errorf("unknown alert type %q", kind)
	}
	get := func(name string) string { return strings.TrimSpace(f[name]) }
	for _, fd := range p.Fields {
		if fd.Required && get(fd.Name) == "" {
			return "", fmt.Errorf("%s is required", fd.Label)
		}
	}

	var out string
	switch kind {
	case PresetDiscord:
		id, token, err := discordWebhook(get("webhook_url"))
		if err != nil {
			return "", err
		}
		out = "discord://" + id + "/" + token

	case PresetTelegram:
		token, chat := get("bot_token"), get("chat_id")
		if !reTelegramBot.MatchString(token) {
			return "", errors.New("the bot token should look like 123456789:AA… (copy it from @BotFather)")
		}
		if !reTelegramChat.MatchString(chat) {
			return "", errors.New("the chat id should be a number (a group's starts with -) or an @channel name")
		}
		out = "tgram://" + token + "/" + chat

	case PresetNtfy:
		scheme, host := "ntfys", "ntfy.sh"
		if s := get("server"); s != "" {
			u, err := url.Parse(s)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
				return "", errors.New("the ntfy server should be an address like https://ntfy.example.com")
			}
			if u.Scheme == "http" {
				scheme = "ntfy"
			}
			host = u.Host
		}
		topic := get("topic")
		if !reNtfyTopic.MatchString(topic) {
			return "", errors.New("the topic may use letters, numbers, - and _ only (up to 64)")
		}
		auth := ""
		if t := get("token"); t != "" {
			if !reToken.MatchString(t) {
				return "", errors.New("the access token should look like tk_…")
			}
			auth = t + "@"
		}
		out = scheme + "://" + auth + host + "/" + topic

	case PresetPushover:
		user, token := get("user_key"), get("app_token")
		if !rePushoverKey.MatchString(user) {
			return "", errors.New("the user key should be the 30 letters and numbers from your Pushover dashboard")
		}
		if !rePushoverKey.MatchString(token) {
			return "", errors.New("the API token should be the 30 letters and numbers of your Pushover application")
		}
		out = "pover://" + user + "@" + token

	case PresetEmail:
		var err error
		if out, err = emailURL(get, func(name string) string { return f[name] }); err != nil {
			return "", err
		}

	case PresetCustom:
		out = get("url")
	}

	if err := ValidateAppriseURL(out); err != nil {
		return "", err
	}
	return out, nil
}

// discordWebhook reads the id and token from a pasted webhook URL
// (https://discord.com/api/webhooks/{id}/{token}, discordapp.com and the ptb/canary
// hosts too).
func discordWebhook(raw string) (id, token string, err error) {
	bad := errors.New("paste the whole webhook URL, like https://discord.com/api/webhooks/123…/abc…")
	u, perr := url.Parse(raw)
	if perr != nil || u.Scheme != "https" {
		return "", "", bad
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
	default:
		return "", "", bad
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "webhooks" || !reDiscordID.MatchString(parts[2]) || !reToken.MatchString(parts[3]) {
		return "", "", bad
	}
	return parts[2], parts[3], nil
}

// emailURL builds mailto(s)://user:pass@host:port?from=…&to=…&mode=…, every part escaped.
// get reads a field trimmed, raw as typed.
func emailURL(get, raw func(string) string) (string, error) {
	host := get("host")
	if !reHost.MatchString(host) && net.ParseIP(host) == nil {
		return "", errors.New("the SMTP server should be a host name like smtp.gmail.com")
	}
	port := get("port")
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("the port should be a number like 587")
		}
	}
	security := get("security")
	if security == "" {
		security = "starttls"
	}
	scheme := "mailtos"
	switch security {
	case "starttls", "ssl":
	case "none":
		scheme = "mailto"
	default:
		return "", errors.New("security should be starttls, ssl or none")
	}
	from := get("from")
	if !looksLikeEmail(from) {
		return "", errors.New("From should be an email address")
	}
	var to []string
	for _, t := range strings.Split(get("to"), ",") {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		if !looksLikeEmail(t) {
			return "", errors.New("To should be one or more email addresses, comma-separated")
		}
		to = append(to, t)
	}
	if len(to) == 0 {
		return "", errors.New("To is required")
	}

	u := url.URL{Scheme: scheme, Host: host}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	// The password isn't trimmed: spaces at either end of one are part of it.
	if user, pass := get("username"), raw("password"); user != "" {
		if pass != "" {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(user)
		}
	} else if pass != "" {
		return "", errors.New("a password needs a username")
	}
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", strings.Join(to, ","))
	if scheme == "mailtos" {
		q.Set("mode", security)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func looksLikeEmail(s string) bool {
	at := strings.LastIndex(s, "@")
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t\r\n,<>") && strings.Contains(s[at:], ".")
}

// presetConfig is a preset connection's Config: the fields its URL was built from.
type presetConfig struct {
	Fields map[string]string `json:"fields"`
}

// PresetFields returns the fields a connection's URL was built from, or nil when it was
// set up as a raw URL (custom, or any connection made before presets).
func PresetFields(c Connection) map[string]string {
	if !IsFieldPreset(c.Kind) || len(c.Config) == 0 {
		return nil
	}
	var pc presetConfig
	if json.Unmarshal(c.Config, &pc) != nil || len(pc.Fields) == 0 {
		return nil
	}
	return pc.Fields
}

// PresetConfigFor is the Config that keeps a preset's fields: only the fields the preset
// defines, so nothing else a client sends is stored.
func PresetConfigFor(kind string, f map[string]string) json.RawMessage {
	p, _ := PresetFor(kind)
	keep := map[string]string{}
	for _, fd := range p.Fields {
		if v, ok := f[fd.Name]; ok && v != "" {
			keep[fd.Name] = v
		}
	}
	b, _ := json.Marshal(presetConfig{Fields: keep})
	return b
}

// MergeSecrets fills each secret field left blank in the form with the stored value, so
// editing the chat id doesn't mean pasting the bot token again.
func MergeSecrets(kind string, in, stored map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	p, _ := PresetFor(kind)
	for _, fd := range p.Fields {
		if fd.Secret && strings.TrimSpace(out[fd.Name]) == "" && stored[fd.Name] != "" {
			out[fd.Name] = stored[fd.Name]
		}
	}
	return out
}

// MaskedFields is a preset connection's fields as the API shows them: every secret blank
// with "<name>_set": true beside it, the rest as saved. Sorted keys keep it stable.
func MaskedFields(kind string, f map[string]string) map[string]any {
	if f == nil {
		return nil
	}
	p, _ := PresetFor(kind)
	secret := map[string]bool{}
	for _, fd := range p.Fields {
		secret[fd.Name] = fd.Secret
	}
	names := make([]string, 0, len(p.Fields))
	for _, fd := range p.Fields {
		names = append(names, fd.Name)
	}
	sort.Strings(names)
	out := map[string]any{}
	for _, n := range names {
		v := f[n]
		if secret[n] {
			out[n] = ""
			out[n+"_set"] = v != ""
			continue
		}
		out[n] = v
	}
	return out
}
