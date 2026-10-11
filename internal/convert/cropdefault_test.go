package convert

import (
	"context"
	"testing"
)

// Black-bar crop is off on an install that never chose, and a saved choice is respected
// either way.
func TestCropOffUnlessChosen(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	if s.prefs(ctx).crop {
		t.Fatal("crop must be off when the setting was never saved")
	}
	if s.GetSettings(ctx).Crop {
		t.Fatal("the settings page must show crop off by default")
	}
	set(t, s, map[string]string{keyCrop: "true"})
	if !s.prefs(ctx).crop {
		t.Fatal("crop turned on must stay on")
	}
	set(t, s, map[string]string{keyCrop: "false"})
	if s.prefs(ctx).crop {
		t.Fatal("crop turned off must stay off")
	}
}
