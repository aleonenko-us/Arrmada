package health

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBundledQbitCheck(t *testing.T) {
	run := func(broad []string, oldSeed bool, err error) []Finding {
		return BundledQbitCheck(func(context.Context) ([]string, bool, error) { return broad, oldSeed, err }).Run(context.Background())
	}
	if f := run(nil, false, nil); len(f) != 0 {
		t.Errorf("nothing broad: %+v", f)
	}
	if f := run(nil, false, errors.New("unreachable")); len(f) != 0 {
		t.Errorf("unreachable is the download clients check's to say: %+v", f)
	}
	f := run([]string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}, true, nil)
	if len(f) != 1 || f[0].Level != LevelWarning || f[0].Fix != FixDownloadClients ||
		!strings.Contains(f[0].Message, "192.168.0.0/16") || !strings.Contains(f[0].Message, "first installed with") {
		t.Errorf("old seed: %+v", f)
	}
	f = run([]string{"10.0.0.0/8"}, false, nil)
	if len(f) != 1 || !strings.Contains(f[0].Message, "Tools → Options → WebUI") {
		t.Errorf("an owner's broad whitelist: %+v", f)
	}
}
