package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/jellyfin"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
)

// Live Sign in with Jellyfin against a real, throwaway Jellyfin (scripts/jellyfin-it.sh):
//
//	JELLYFIN_IT_PKGS=./internal/httpapi/... scripts/jellyfin-it.sh -run IT_Jellyfin

type itKeys map[string]string

func (k itKeys) Value(_ context.Context, id string) string     { return k[id] }
func (k itKeys) Set(_ context.Context, id, value string) error { k[id] = value; return nil }

type jfIT struct {
	s                  *routeServer
	url, key           string
	user, userPw       string
	adminUser, adminPw string
	device             string
}

func jellyfinIT(t *testing.T) *jfIT {
	t.Helper()
	if os.Getenv("ARRMADA_JELLYFIN_IT") != "1" {
		t.Skip("live Jellyfin test: run scripts/jellyfin-it.sh (ARRMADA_JELLYFIN_IT=1)")
	}
	e := &jfIT{
		url: os.Getenv("ARRMADA_JELLYFIN_URL"), key: os.Getenv("ARRMADA_JELLYFIN_KEY"),
		user: os.Getenv("ARRMADA_JELLYFIN_USER"), userPw: os.Getenv("ARRMADA_JELLYFIN_PASSWORD"),
		adminUser: os.Getenv("ARRMADA_JELLYFIN_ADMIN_USER"), adminPw: os.Getenv("ARRMADA_JELLYFIN_ADMIN_PASSWORD"),
	}
	if e.url == "" || e.key == "" || e.user == "" {
		t.Fatal("the ARRMADA_JELLYFIN_* variables must be set")
	}
	e.s = newRouteServer(t, func(d *Deps) {
		d.Jellyfin = jellyfinsvc.New(jellyfinsvc.Options{Settings: d.Settings, Keys: itKeys{apikeys.Jellyfin: e.key}, Version: "it", Log: d.Log})
	})
	ctx := context.Background()
	_ = e.s.deps.Settings.Set(ctx, jellyfinsvc.KeyURL, e.url)
	_ = e.s.deps.Settings.SetBool(ctx, keyJellyfinLoginEnabled, true)
	e.device = e.s.deps.Jellyfin.DeviceID(ctx)
	return e
}

// lingering lists the devices Jellyfin holds a token for under Arrmada's device id.
func (e *jfIT) lingering(t *testing.T) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+"/Devices", nil)
	req.Header.Set("Authorization", `MediaBrowser Token="`+e.key+`", Client="arrmada-it", Device="it", DeviceId="arrmada-it-devices", Version="0.1"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Items []struct {
			ID           string `json:"Id"`
			AppName      string `json:"AppName"`
			LastUserName string `json:"LastUserName"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET /Devices: HTTP %d: %v", resp.StatusCode, err)
	}
	var mine []string
	for _, d := range out.Items {
		if d.ID == e.device {
			mine = append(mine, d.AppName+" as "+d.LastUserName)
		}
	}
	t.Logf("Jellyfin lists %d devices, %d under Arrmada's id %s", len(out.Items), len(mine), e.device)
	return mine
}

// A username and password sign-in makes the requester and links the Jellyfin user; the
// same person next time lands in the same account; nothing stays signed in in Jellyfin.
func TestIT_JellyfinPasswordSignIn(t *testing.T) {
	e := jellyfinIT(t)
	ctx := context.Background()
	// The control: a sign-in under Arrmada's device id that isn't logged out does show up,
	// so an empty list below means the logout worked, not that the check can't see.
	c := e.s.deps.Jellyfin.Client(ctx)
	raw, err := c.AuthenticateByName(ctx, e.user, e.userPw)
	if err != nil {
		t.Fatal(err)
	}
	if left := e.lingering(t); len(left) != 1 {
		t.Fatalf("a sign-in left signed in shows %v under Arrmada's id, want one device", left)
	}
	if err := c.WithToken(raw.AccessToken).Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if rec := jfLogin(e.s, lan, e.user, e.userPw+"-wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: HTTP %d %s", rec.Code, rec.Body)
	}
	u := signedInAs(t, jfLogin(e.s, lan, e.user, e.userPw))
	if !u.JellyfinLinked || !strings.EqualFold(u.JellyfinUsername, e.user) || u.Role != "requester" {
		t.Errorf("account = %+v", u)
	}
	jfID := e.s.auth.JellyfinIDForUser(ctx, u.ID)
	users, err := e.s.deps.Jellyfin.Client(ctx).Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ju := range users {
		found = found || (ju.ID == jfID && strings.EqualFold(ju.Name, e.user))
	}
	if !found {
		t.Errorf("linked id %q isn't the Jellyfin user %s", jfID, e.user)
	}
	if again := signedInAs(t, jfLogin(e.s, lan, e.user, e.userPw)); again.ID != u.ID {
		t.Errorf("second sign-in landed in %d, want %d", again.ID, u.ID)
	}
	if left := e.lingering(t); len(left) != 0 {
		t.Errorf("Jellyfin still lists Arrmada devices after sign-in: %v", left)
	}
	// The Jellyfin administrator is never made a requester.
	if e.adminUser != "" {
		rec := jfLogin(e.s, lan, e.adminUser, e.adminPw)
		if rec.Code != http.StatusConflict {
			t.Errorf("Jellyfin admin: HTTP %d %s, want 409", rec.Code, rec.Body)
		}
		if left := e.lingering(t); len(left) != 0 {
			t.Errorf("the refused admin sign-in left a device: %v", left)
		}
	}
}

// Quick Connect end to end: Arrmada starts it, the test user approves the code from
// another Jellyfin app (over the API), Arrmada's poll signs in once, and a second poll
// with the same id gets nothing.
func TestIT_JellyfinQuickConnectSignIn(t *testing.T) {
	e := jellyfinIT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rec := e.s.doFrom("POST", "/api/v1/auth/jellyfin/quickconnect", "", lan)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: HTTP %d %s", rec.Code, rec.Body)
	}
	var out struct{ ID, Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	cookie := cookieNamed(rec, jfQCCookieName(jfFlowLogin))
	poll := "/api/v1/auth/jellyfin/quickconnect/" + out.ID
	if rec := e.s.doFrom("GET", poll, "", lan, cookie); !strings.Contains(rec.Body.String(), `"pending":true`) {
		t.Fatalf("before approval: HTTP %d %s", rec.Code, rec.Body)
	}

	// The person, signed in to Jellyfin on their phone, types the code in.
	phone := jellyfin.New(e.url, "", "arrmada-it-phone", "0.1-it", nil)
	a, err := phone.AuthenticateByName(ctx, e.user, e.userPw)
	if err != nil {
		t.Fatal(err)
	}
	signedPhone := phone.WithToken(a.AccessToken)
	defer func() { _ = signedPhone.Logout(context.WithoutCancel(ctx)) }()
	if err := signedPhone.QuickConnectAuthorize(ctx, out.Code); err != nil {
		t.Fatal(err)
	}

	u := signedInAs(t, e.s.doFrom("GET", poll, "", lan, cookie))
	if !strings.EqualFold(u.JellyfinUsername, e.user) || e.s.auth.JellyfinIDForUser(ctx, u.ID) != a.User.ID {
		t.Errorf("Quick Connect signed in as %+v, want %s (%s)", u, e.user, a.User.ID)
	}
	if rec := e.s.doFrom("GET", poll, "", lan, cookie); rec.Code != http.StatusForbidden {
		t.Errorf("second poll: HTTP %d %s, want 403", rec.Code, rec.Body)
	}
	if left := e.lingering(t); len(left) != 0 {
		t.Errorf("Jellyfin still lists Arrmada devices after Quick Connect: %v", left)
	}
}
