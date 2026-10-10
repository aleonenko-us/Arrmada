package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/notify"
)

const tgTestToken = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"

// OBS-16: a Telegram connection made from its form stores the built URL; the list shows
// the chat id but never the bot token; an edit that leaves the token blank keeps it, and
// one that changes the chat id rebuilds the URL with the stored token.
func TestPresetUpdateKeepsSecret(t *testing.T) {
	s, sent := newNotifyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	ctx := context.Background()

	rec := s.doJSON("POST", "/api/v1/notifications", admin, `{"name":"Phone","kind":"telegram","enabled":true,"fields":{"bot_token":"`+tgTestToken+`","chat_id":"111222333"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: HTTP %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "AAHdqTcv") {
		t.Fatalf("the create answer echoes the token: %s", rec.Body)
	}
	c, _ := s.deps.Notify.Get(ctx, 1)
	if c.URL != "tgram://"+tgTestToken+"/111222333" {
		t.Fatalf("stored URL = %q", c.URL)
	}

	for _, who := range []*http.Cookie{admin, mgr} {
		list := listNotifications(t, s, who)
		raw, _ := json.Marshal(list)
		if strings.Contains(string(raw), "AAHdqTcv") || strings.Contains(string(raw), `"url":`) {
			t.Fatalf("the list carries the token: %s", raw)
		}
		fields, _ := list[0]["fields"].(map[string]any)
		if list[0]["preset"] != "telegram" || fields["chat_id"] != "111222333" || fields["bot_token"] != "" || fields["bot_token_set"] != true {
			t.Fatalf("list entry = %+v", list[0])
		}
	}

	// A new chat id, token left blank: the stored token is kept.
	rec = s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"Phone","kind":"telegram","enabled":true,"fields":{"bot_token":"","chat_id":"-100999"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, _ = s.deps.Notify.Get(ctx, 1); c.URL != "tgram://"+tgTestToken+"/-100999" {
		t.Fatalf("after update URL = %q", c.URL)
	}
	// Renaming without fields keeps the preset and its URL.
	if rec = s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"Renamed","kind":"telegram","enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, _ = s.deps.Notify.Get(ctx, 1); c.URL != "tgram://"+tgTestToken+"/-100999" || notify.PresetFields(c)["bot_token"] != tgTestToken {
		t.Fatalf("after rename: %q %s", c.URL, c.Config)
	}

	// Testing an unsaved edit by id fills the blank token server-side.
	rec = s.doJSON("POST", "/api/v1/notifications/test", admin, `{"id":1,"name":"x","kind":"telegram","fields":{"bot_token":"","chat_id":"42"}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("test: HTTP %d %s", rec.Code, rec.Body)
	}
	if got := sent.sent(); len(got) != 1 || got[0] != "tgram://"+tgTestToken+"/42" {
		t.Fatalf("sent to %v", got)
	}

	// Switching to another preset never carries the old secret across.
	rec = s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"x","kind":"pushover","enabled":true,"fields":{"user_key":"","app_token":"azGDORePK8gMaC0QOYAMyEEuzJnyUi"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("switch with a blank secret: HTTP %d %s", rec.Code, rec.Body)
	}
	// A broken field is refused by name and nothing changes.
	rec = s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"x","kind":"telegram","enabled":true,"fields":{"bot_token":"nope","chat_id":"1"}}`)
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("bad token: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, _ = s.deps.Notify.Get(ctx, 1); c.URL != "tgram://"+tgTestToken+"/-100999" {
		t.Fatalf("a refused update changed the URL: %q", c.URL)
	}
}

// A Discord alert from the pasted webhook URL alone; the list shows neither the webhook
// nor its token, just that one is saved.
func TestPresetDiscordFromWebhook(t *testing.T) {
	s, _ := newNotifyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	rec := s.doJSON("POST", "/api/v1/notifications", admin, `{"name":"Discord","kind":"discord","enabled":true,"fields":{"webhook_url":"https://discord.com/api/webhooks/112233445566/SECRETwebhookTOKEN123"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: HTTP %d %s", rec.Code, rec.Body)
	}
	c, _ := s.deps.Notify.Get(context.Background(), 1)
	if c.URL != "discord://112233445566/SECRETwebhookTOKEN123" {
		t.Fatalf("URL = %q", c.URL)
	}
	raw := s.do("GET", "/api/v1/notifications", admin).Body.String()
	if strings.Contains(raw, "SECRETwebhookTOKEN") || strings.Contains(raw, "112233445566/") || strings.Contains(raw, "discord.com/api") {
		t.Fatalf("the list leaks the webhook: %s", raw)
	}
	if !strings.Contains(raw, `"webhook_url_set":true`) {
		t.Fatalf("the list should say the webhook is saved: %s", raw)
	}
}

// Old raw-URL connections are labelled custom and keep working; their URL comes back
// only as a masked hint, and sending that hint back (or nothing) keeps the stored URL.
func TestListMasksCustomURL(t *testing.T) {
	s, _ := newNotifyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	// kind "discord" with a raw URL: how every connection was made before presets.
	if rec := s.doJSON("POST", "/api/v1/notifications", admin, `{"name":"Old","kind":"discord","url":"discord://123456/LEGACYSECRETTOKEN","enabled":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: HTTP %d %s", rec.Code, rec.Body)
	}
	list := listNotifications(t, s, admin)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "LEGACYSECRET") || strings.Contains(string(raw), "123456/") {
		t.Fatalf("list leaks the URL: %s", raw)
	}
	hint, _ := list[0]["url_hint"].(string)
	if list[0]["preset"] != "custom" || list[0]["fields"] != nil || hint == "" || !strings.HasPrefix(hint, "discord://") {
		t.Fatalf("legacy entry = %+v", list[0])
	}
	body, _ := json.Marshal(map[string]any{"name": "Old", "kind": "discord", "url": hint, "enabled": true})
	if rec := s.doJSON("PUT", "/api/v1/notifications/1", admin, string(body)); rec.Code != http.StatusOK {
		t.Fatalf("update with the hint: HTTP %d %s", rec.Code, rec.Body)
	}
	if c, _ := s.deps.Notify.Get(context.Background(), 1); c.URL != "discord://123456/LEGACYSECRETTOKEN" {
		t.Fatalf("the masked hint replaced the URL: %q", c.URL)
	}

	// A preset connection edited into a custom one with a new raw URL drops its fields.
	if rec := s.doJSON("POST", "/api/v1/notifications", admin, `{"name":"P","kind":"pushover","enabled":true,"fields":{"user_key":"uQiRzpo4DXghDmr9QzzfQu27cmVRsG","app_token":"azGDORePK8gMaC0QOYAMyEEuzJnyUi"}}`); rec.Code != http.StatusCreated {
		t.Fatalf("create pushover: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("PUT", "/api/v1/notifications/2", admin, `{"name":"P","kind":"custom","url":"gotify://gotify.example.com/NEWTOKEN","enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("to custom: HTTP %d %s", rec.Code, rec.Body)
	}
	c, _ := s.deps.Notify.Get(context.Background(), 2)
	if c.URL != "gotify://gotify.example.com/NEWTOKEN" || notify.PresetFields(c) != nil || strings.Contains(string(c.Config), "uQiRzpo") {
		t.Fatalf("after switching to custom: %q %s", c.URL, c.Config)
	}
}

// The tiles the page shows, each with its form.
func TestNotificationPresetsList(t *testing.T) {
	s, _ := newNotifyServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	rec := s.do("GET", "/api/v1/notifications/presets", mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Presets []notify.PresetDef `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, p := range out.Presets {
		kinds[p.Kind] = true
	}
	for _, k := range []string{"discord", "telegram", "ntfy", "pushover", "email", "custom", "webpush"} {
		if !kinds[k] {
			t.Errorf("preset %q missing", k)
		}
	}
}
