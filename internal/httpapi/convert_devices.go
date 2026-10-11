package httpapi

import (
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/insights"
)

// devicesWindow is how far back the devices panel looks: long enough to have seen the
// household's players, short enough that a TV replaced last year no longer counts.
const devicesWindow = 90 * 24 * time.Hour

// convertDevices is GET /api/v1/convert/devices.
type convertDevices struct {
	// Monitoring says Insights is recording plays at all; without it the list can only be
	// as old as the last time it was on (or empty), and the panel says so.
	Monitoring bool                     `json:"monitoring"`
	Since      int64                    `json:"since"`
	Devices    []insights.DeviceSupport `json:"devices"`
}

// handleConvertDevices — GET /api/v1/convert/devices: which players direct-played HEVC,
// HEVC 10-bit and AV1 in the last 90 days (Convert's "Your devices" panel, and the warnings
// before AV1 or 10-bit output). Device names are household data: manager-only.
func (a *api) handleConvertDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := time.Now().Add(-devicesWindow)
	out := convertDevices{Since: since.Unix(), Devices: []insights.DeviceSupport{}}
	if a.deps.Insights != nil {
		out.Monitoring = a.deps.Insights.MonitoringActive(ctx)
		list, err := a.deps.Insights.DeviceCodecSupport(ctx, since)
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not read the recorded plays")
			return
		}
		out.Devices = list
	}
	a.writeJSON(w, http.StatusOK, out)
}
