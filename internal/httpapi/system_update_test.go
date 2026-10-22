package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/updatecheck"
)

// Status carries the update check's answer; switching the check off stops every request
// to (a fake) GitHub, and Check now then says it's off.
func TestUpdateCheckRoutes(t *testing.T) {
	var hits atomic.Int32
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/aleonenko-us/Arrmada/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://github.com/aleonenko-us/Arrmada/releases/tag/v1.3.0","body":"* Faster"}`))
	}))
	defer gh.Close()

	a := settingsAPI(t)
	a.deps.Updates = updatecheck.New(updatecheck.Options{
		Client: gh.Client(), BaseURL: gh.URL, Version: "v1.2.0", Commit: "abc1234",
		Enabled: func(ctx context.Context) bool { return a.deps.Settings.GetBool(ctx, updatecheck.KeyEnabled, true) },
	})

	call := func(h http.HandlerFunc, method, body string) (int, updatecheck.Status) {
		t.Helper()
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(method, "/", strings.NewReader(body)))
		var st updatecheck.Status
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, st
	}

	// On by default: Check now finds the newer release.
	code, st := call(a.handleUpdateCheckNow, http.MethodPost, "")
	if code != http.StatusOK || !st.Enabled || !st.Available || st.Latest == nil || st.Latest.Tag != "v1.3.0" {
		t.Fatalf("check now: %d %+v", code, st)
	}

	// /system/status carries the same answer.
	w := httptest.NewRecorder()
	a.handleSystemStatus(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var status struct {
		Update *updatecheck.Status `json:"update"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.Update == nil || !status.Update.Available {
		t.Fatalf("status update = %+v (%v): %s", status.Update, err, w.Body)
	}

	// Off: saved, the answer says so, and GitHub hears nothing more.
	before := hits.Load()
	if code, st := call(a.handleSetUpdateCheck, http.MethodPut, `{"enabled":false}`); code != http.StatusOK || st.Enabled || st.Latest != nil {
		t.Fatalf("switch off: %d %+v", code, st)
	}
	if a.deps.Settings.GetBool(t.Context(), updatecheck.KeyEnabled, true) {
		t.Error("setting not saved")
	}
	if code, _ := call(a.handleUpdateCheckNow, http.MethodPost, ""); code != http.StatusConflict {
		t.Errorf("check now while off: %d, want 409", code)
	}
	_ = a.deps.Updates.Run(t.Context())
	if hits.Load() != before {
		t.Errorf("%d requests to GitHub while off", hits.Load()-before)
	}

	if code, _ := call(a.handleSetUpdateCheck, http.MethodPut, `{}`); code != http.StatusBadRequest {
		t.Errorf("no enabled: %d, want 400", code)
	}
	if code, st := call(a.handleSetUpdateCheck, http.MethodPut, `{"enabled":true}`); code != http.StatusOK || !st.Enabled {
		t.Errorf("switch on: %d %+v", code, st)
	}
}
