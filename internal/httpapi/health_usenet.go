package httpapi

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/health"
)

// The usenet client's health checks (internal/health/checks_usenet.go): what SABnzbd says
// about itself, whether it's downloading anything, and whether Arrmada can tidy up after it.

func (a *api) registerUsenetChecks(reg *health.Registry) {
	reg.Register(health.UsenetClientsCheck(a.usenetClientFacts))
	if a.deps.Automation == nil {
		return
	}
	reg.Register(health.UsenetProgressCheck(func(ctx context.Context) []health.UsenetIdle {
		names := a.downloadClientNames(ctx)
		var out []health.UsenetIdle
		for _, u := range a.deps.Automation.UsenetIdle() {
			out = append(out, health.UsenetIdle{ClientID: u.ClientID, ClientName: names.of(u.ClientID), Since: u.Since, Jobs: u.Jobs})
		}
		return out
	}, nil))
	reg.Register(health.UsenetLeftoversCheck(func(ctx context.Context) []health.UsenetLeftovers {
		names := a.downloadClientNames(ctx)
		var out []health.UsenetLeftovers
		for _, l := range a.deps.Automation.UsenetLeftovers() {
			out = append(out, health.UsenetLeftovers{ClientID: l.ClientID, ClientName: names.of(l.ClientID),
				Count: l.Count, Name: l.Example.Name, Detail: l.Example.Detail})
		}
		return out
	}))
}

// usenetClientFacts reads each enabled usenet client's facts. One that can't be read
// carries its error: the client check already says it's unreachable.
func (a *api) usenetClientFacts(ctx context.Context) ([]health.UsenetClient, error) {
	all, err := a.deps.Downloads.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []health.UsenetClient
	for _, c := range all {
		if !c.Enabled || c.Kind.Transport() != download.TransportUsenet {
			continue
		}
		f, err := a.deps.Downloads.UsenetFacts(ctx, c.ID)
		uc := health.UsenetClient{ID: c.ID, Name: c.Name, Facts: f, Err: err}
		if c.Bundled && c.Kind == download.KindSABnzbd {
			uc.BundledPort = sabUIPort()
		}
		out = append(out, uc)
	}
	return out, nil
}

// clientNames is the download clients' names by id.
type clientNames map[int64]string

// of is a client's name, or a plain stand-in for one removed since.
func (n clientNames) of(id int64) string {
	if name := n[id]; name != "" {
		return name
	}
	return "SABnzbd"
}

func (a *api) downloadClientNames(ctx context.Context) clientNames {
	out := clientNames{}
	if all, err := a.deps.Downloads.List(ctx); err == nil {
		for _, c := range all {
			out[c.ID] = c.Name
		}
	}
	return out
}
