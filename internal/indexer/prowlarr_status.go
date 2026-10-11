package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Errors ProwlarrStatus returns before asking anything.
var (
	ErrProwlarrNoURL = errors.New("no Prowlarr URL is set")
	ErrProwlarrNoKey = errors.New("no Prowlarr API key is saved; sync once with the key from Prowlarr → Settings → General")
)

// ProwlarrStatus asks Prowlarr who it is (GET /api/v1/system/status) and returns its
// version. It's the Connections card's Test: one cheap, authenticated request that proves
// both the address and the key, without syncing anything.
func ProwlarrStatus(ctx context.Context, base, key string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", ErrProwlarrNoURL
	}
	if key == "" {
		return "", ErrProwlarrNoKey
	}
	b, err := prowlarrDo(ctx, base, key, http.MethodGet, "/api/v1/system/status", nil)
	if err != nil {
		return "", err
	}
	var st struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return "", errors.New("prowlarr: unexpected answer (is this address Prowlarr?)")
	}
	return st.Version, nil
}
