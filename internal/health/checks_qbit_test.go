package health

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBundledQbitCheck(t *testing.T) {
	run := func(e QbitExposure, err error) []Finding {
		return BundledQbitCheck(func(context.Context) (QbitExposure, error) { return e, err }).Run(context.Background())
	}
	if f := run(QbitExposure{}, nil); len(f) != 0 {
		t.Errorf("nothing broad: %+v", f)
	}
	if f := run(QbitExposure{}, errors.New("unreachable")); len(f) != 0 {
		t.Errorf("unreachable is the download clients check's to say: %+v", f)
	}
	seed := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	f := run(QbitExposure{Broad: seed, OldSeed: true}, nil)
	if len(f) != 1 || f[0].Level != LevelWarning || f[0].Fix != FixDownloadClients ||
		!strings.Contains(f[0].Message, "192.168.0.0/16") || !strings.Contains(f[0].Message, "first installed with") {
		t.Errorf("old seed: %+v", f)
	}
	f = run(QbitExposure{Broad: []string{"10.0.0.0/8"}}, nil)
	if len(f) != 1 || !strings.Contains(f[0].Message, "Tools → Options → WebUI") {
		t.Errorf("an owner's broad whitelist: %+v", f)
	}
	// Left alone on purpose: says why, not "restart to try again".
	f = run(QbitExposure{Broad: seed, OldSeed: true, Held: "your download client “Mine” may sign in to it without a password, and would be shut out"}, nil)
	if len(f) != 1 || !strings.Contains(f[0].Message, "on purpose") || !strings.Contains(f[0].Message, "“Mine”") || strings.Contains(f[0].Message, "restart") {
		t.Errorf("held: %+v", f)
	}
}
