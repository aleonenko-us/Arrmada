package notify

import (
	"net/url"
	"strings"
	"testing"
)

// Each preset builds the Apprise URL its service expects, escaping what came from the
// form, and refuses missing or malformed fields by name without echoing their values.
func TestBuildURL(t *testing.T) {
	tgToken := "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	cases := []struct {
		name    string
		kind    string
		fields  map[string]string
		want    string
		wantErr string
	}{
		{"discord", PresetDiscord, map[string]string{"webhook_url": " https://discord.com/api/webhooks/112233445566/AbC-dEf_GhI123456789 "}, "discord://112233445566/AbC-dEf_GhI123456789", ""},
		{"discord legacy host", PresetDiscord, map[string]string{"webhook_url": "https://discordapp.com/api/webhooks/112233445566/AbC-dEf_GhI123456789"}, "discord://112233445566/AbC-dEf_GhI123456789", ""},
		{"discord not a webhook", PresetDiscord, map[string]string{"webhook_url": "https://evil.example/api/webhooks/1122334/abcdefghij"}, "", "whole webhook URL"},
		{"discord missing", PresetDiscord, map[string]string{}, "", "Webhook URL is required"},
		{"telegram", PresetTelegram, map[string]string{"bot_token": tgToken, "chat_id": "-1001234567890"}, "tgram://" + tgToken + "/-1001234567890", ""},
		{"telegram channel", PresetTelegram, map[string]string{"bot_token": tgToken, "chat_id": "@arrmada_alerts"}, "tgram://" + tgToken + "/@arrmada_alerts", ""},
		{"telegram bad token", PresetTelegram, map[string]string{"bot_token": "nope", "chat_id": "1"}, "", "bot token"},
		{"telegram missing chat", PresetTelegram, map[string]string{"bot_token": tgToken}, "", "Chat id is required"},
		{"ntfy.sh", PresetNtfy, map[string]string{"topic": "arrmada-7f3k2q"}, "ntfys://ntfy.sh/arrmada-7f3k2q", ""},
		{"ntfy own server with token", PresetNtfy, map[string]string{"server": "https://ntfy.example.com:8443/", "topic": "alerts", "token": "tk_abcdefghijkl"}, "ntfys://tk_abcdefghijkl@ntfy.example.com:8443/alerts", ""},
		{"ntfy plain http", PresetNtfy, map[string]string{"server": "http://192.168.1.5", "topic": "alerts"}, "ntfy://192.168.1.5/alerts", ""},
		{"ntfy bad topic", PresetNtfy, map[string]string{"topic": "a/b"}, "", "topic"},
		{"ntfy server with a path", PresetNtfy, map[string]string{"server": "https://x.example/ntfy", "topic": "t"}, "", "ntfy server"},
		{"pushover", PresetPushover, map[string]string{"user_key": "uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "app_token": "azGDORePK8gMaC0QOYAMyEEuzJnyUi"}, "pover://uQiRzpo4DXghDmr9QzzfQu27cmVRsG@azGDORePK8gMaC0QOYAMyEEuzJnyUi", ""},
		{"pushover bad key", PresetPushover, map[string]string{"user_key": "short", "app_token": "azGDORePK8gMaC0QOYAMyEEuzJnyUi"}, "", "user key"},
		{"custom", PresetCustom, map[string]string{"url": "gotify://host/token"}, "gotify://host/token", ""},
		{"custom invalid", PresetCustom, map[string]string{"url": "-c /etc/x"}, "", "must not start"},
		{"webpush has no URL", KindWebPush, map[string]string{}, "", "unknown"},
		{"unknown", "carrier-pigeon", map[string]string{}, "", "unknown"},
	}
	for _, c := range cases {
		got, err := BuildURL(c.kind, c.fields)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want one mentioning %q", c.name, err, c.wantErr)
			}
			for _, v := range c.fields {
				if len(v) > 6 && err != nil && strings.Contains(err.Error(), v) {
					t.Errorf("%s: the error quotes a field value: %v", c.name, err)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A password with "@", "/", ":" and "#" still points at the right server, and reads
// back exactly as typed once the URL is parsed (Apprise unquotes the user info).
func TestBuildURLEmailEscaping(t *testing.T) {
	pass := "p@ss/w:rd#1 "
	got, err := BuildURL(PresetEmail, map[string]string{
		"host": "smtp.example.com", "port": "587", "username": "me@example.com", "password": pass,
		"from": "me@example.com", "to": "you@example.com, them@example.org",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "mailtos://") {
		t.Fatalf("got %q, want mailtos://", got)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("built URL doesn't parse: %v", err)
	}
	if u.Hostname() != "smtp.example.com" || u.Port() != "587" {
		t.Fatalf("host = %q port = %q — the password changed where it points", u.Hostname(), u.Port())
	}
	if p, _ := u.User.Password(); p != pass || u.User.Username() != "me@example.com" {
		t.Fatalf("userinfo reads back as %q / %q", u.User.Username(), p)
	}
	q := u.Query()
	if q.Get("from") != "me@example.com" || q.Get("to") != "you@example.com,them@example.org" || q.Get("mode") != "starttls" {
		t.Fatalf("query = %v", q)
	}

	plain, err := BuildURL(PresetEmail, map[string]string{"host": "mail.lan", "security": "none", "from": "a@b.lan", "to": "c@d.lan"})
	if err != nil || !strings.HasPrefix(plain, "mailto://mail.lan?") || strings.Contains(plain, "mode=") {
		t.Fatalf("no TLS: %q, %v", plain, err)
	}
	for name, f := range map[string]map[string]string{
		"bad port":          {"host": "h.example", "port": "99999", "from": "a@b.c", "to": "c@d.e"},
		"bad from":          {"host": "h.example", "from": "nobody", "to": "c@d.e"},
		"bad to":            {"host": "h.example", "from": "a@b.c", "to": "c@d.e, nope"},
		"host with a path":  {"host": "h.example/x", "from": "a@b.c", "to": "c@d.e"},
		"password, no user": {"host": "h.example", "password": "x", "from": "a@b.c", "to": "c@d.e"},
		"missing to":        {"host": "h.example", "from": "a@b.c"},
	} {
		if _, err := BuildURL(PresetEmail, f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A secret left blank keeps the stored one; a typed one replaces it; non-secret fields
// always come from the form.
func TestMergeSecrets(t *testing.T) {
	stored := map[string]string{"bot_token": "stored-token", "chat_id": "111"}
	got := MergeSecrets(PresetTelegram, map[string]string{"bot_token": " ", "chat_id": "222"}, stored)
	if got["bot_token"] != "stored-token" || got["chat_id"] != "222" {
		t.Fatalf("blank secret: %+v", got)
	}
	got = MergeSecrets(PresetTelegram, map[string]string{"bot_token": "new-token", "chat_id": ""}, stored)
	if got["bot_token"] != "new-token" || got["chat_id"] != "" {
		t.Fatalf("new secret, cleared chat: %+v", got)
	}
	if got := MergeSecrets(PresetTelegram, map[string]string{"chat_id": "1"}, nil); got["bot_token"] != "" {
		t.Fatalf("nothing stored: %+v", got)
	}
}

// The API's view of a preset's fields: secrets blank with _set beside them.
func TestMaskedFields(t *testing.T) {
	got := MaskedFields(PresetNtfy, map[string]string{"server": "https://ntfy.example.com", "topic": "secret-topic"})
	if got["server"] != "https://ntfy.example.com" || got["topic"] != "" || got["topic_set"] != true || got["token_set"] != false {
		t.Fatalf("masked = %+v", got)
	}
	if MaskedFields(PresetNtfy, nil) != nil {
		t.Fatal("no fields should mask to nil")
	}
}

// Only the fields a preset defines are kept, and a raw-URL connection has none.
func TestPresetConfig(t *testing.T) {
	cfg := PresetConfigFor(PresetPushover, map[string]string{"user_key": "u", "app_token": "a", "extra": "junk"})
	c := Connection{Kind: PresetPushover, Config: cfg}
	f := PresetFields(c)
	if f["user_key"] != "u" || f["app_token"] != "a" || f["extra"] != "" {
		t.Fatalf("fields = %+v", f)
	}
	if PresetFields(Connection{Kind: PresetDiscord, Config: []byte(`{}`)}) != nil {
		t.Fatal("a raw-URL discord connection has no fields")
	}
	if PresetFields(Connection{Kind: KindWebPush, Config: []byte(`{"user_id":3}`)}) != nil {
		t.Fatal("a push connection has no fields")
	}
	if !IsFieldPreset(PresetEmail) || IsFieldPreset(PresetCustom) || IsFieldPreset(KindWebPush) || IsFieldPreset("") {
		t.Fatal("IsFieldPreset")
	}
}
