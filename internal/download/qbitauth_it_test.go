package download

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Against a real, freshly seeded Compose qBittorrent (ARRMADA_QBIT_SEED_IT=<its URL>): a
// throwaway container whose config docker/qbittorrent/init.sh wrote, run with this test on
// the same Docker network, so the whitelist is that network's subnet only. Before: anonymous
// requests from this network get in. After SecureBundledQbit: they don't, the kept login
// does, CSRF protection is on, and a second run changes nothing.
func TestSecureBundledQbitSeededLive(t *testing.T) {
	base := os.Getenv("ARRMADA_QBIT_SEED_IT")
	if base == "" {
		t.Skip("set ARRMADA_QBIT_SEED_IT to a throwaway qBittorrent seeded by docker/qbittorrent/init.sh to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	svc, _ := newTestService(t)
	e, err := svc.BundledQbitExposure(ctx, base)
	if err != nil || !e.Open || e.OldSeed || len(e.Broad) != 0 {
		t.Fatalf("the seeded config: %+v, %v (want open to this network only)", e, err)
	}
	p, _, _ := newQbitAuthAPI(base).anonymousPrefs(ctx)
	if !svc.isDockerWhitelist(ctx, base, p.Whitelist) {
		t.Fatalf("the seeded whitelist %q isn't this network's own subnet (%v)", p.Whitelist, svc.localNets())
	}
	registerBundled(t, svc, base, "", "")
	k := &memKeeper{}
	out, err := svc.SecureBundledQbit(ctx, base, k)
	if err != nil || out != SecureNewPassword {
		t.Fatalf("SecureBundledQbit = %v, %v", out, err)
	}
	resp, err := http.Get(strings.TrimRight(base, "/") + "/api/v2/app/preferences")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("an anonymous request got HTTP %d after the switch", resp.StatusCode)
	}
	sess, res, err := newQbitAuthAPI(base).login(ctx, k.user, k.pass, "")
	if err != nil || res != loginAccepted {
		t.Fatalf("the kept login: %v, %v", res, err)
	}
	after, err := sess.prefs(ctx)
	if err != nil || after.WhitelistEnabled || after.ProxyEnabled {
		t.Errorf("after: %+v, %v", after, err)
	}
	if e, err := svc.BundledQbitExposure(ctx, base); err != nil || e.Open {
		t.Errorf("exposure after: %+v, %v", e, err)
	}
	if items, err := NewQBittorrent().List(ctx, bundledRow(t, svc)); err != nil {
		t.Errorf("the client can't read qBittorrent with its new login: %v (%d items)", err, len(items))
	}
	if out, err := svc.SecureBundledQbit(ctx, base, k); err != nil || out != SecureAlready || k.made != 1 {
		t.Errorf("second run = %v, %v (made %d)", out, err, k.made)
	}
}
