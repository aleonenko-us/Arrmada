package download

import (
	"context"
	"errors"
)

// The service's side of the usenet client: the settings it needs from the app, and the
// card's facts and "Add missing categories".

// usenetSetup is a client that needs to know Arrmada's movie category and folders.
type usenetSetup interface {
	SetMovieCategory(movie string)
	SetFolders(fn func(ctx context.Context) PathEnv)
}

// usenetInspector is a usenet client the card can read facts from and create categories in.
type usenetInspector interface {
	Facts(ctx context.Context, dc Client) (UsenetFacts, error)
	AddMissingCategories(ctx context.Context, dc Client) ([]string, error)
}

// SetMovieCategory passes the configured movie category (ARRMADA_DOWNLOAD_CATEGORY) to the
// clients that file by it themselves. Call it while wiring.
func (s *Service) SetMovieCategory(movie string) {
	for _, impl := range s.registry.impls {
		if u, ok := impl.(usenetSetup); ok {
			u.SetMovieCategory(movie)
		}
	}
}

// SetFolders wires where Arrmada keeps things (read on every use) for the clients that
// tidy up after themselves: SABnzbd's finished-job folder is deleted only inside them.
func (s *Service) SetFolders(fn func(ctx context.Context) PathEnv) {
	for _, impl := range s.registry.impls {
		if u, ok := impl.(usenetSetup); ok {
			u.SetFolders(fn)
		}
	}
}

// ErrNotUsenet: the client isn't a usenet client, so it has no usenet facts or categories.
var ErrNotUsenet = errors.New("this download client isn't a usenet client")

func (s *Service) inspector(ctx context.Context, id int64) (Client, usenetInspector, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return Client{}, nil, err
	}
	impl, ok := s.registry.For(c.Kind)
	if !ok {
		return Client{}, nil, ErrNotUsenet
	}
	ui, ok := impl.(usenetInspector)
	if !ok {
		return Client{}, nil, ErrNotUsenet
	}
	return c, ui, nil
}

// UsenetFacts reads a usenet client's card facts: categories, completed folder, news
// servers, language. ErrNotUsenet for any other client.
func (s *Service) UsenetFacts(ctx context.Context, id int64) (UsenetFacts, error) {
	c, ui, err := s.inspector(ctx, id)
	if err != nil {
		return UsenetFacts{}, err
	}
	return ui.Facts(ctx, c)
}

// AddMissingCategories creates Arrmada's categories in a usenet client that lacks them and
// names the ones it created. Only ever on the owner's click.
func (s *Service) AddMissingCategories(ctx context.Context, id int64) ([]string, error) {
	c, ui, err := s.inspector(ctx, id)
	if err != nil {
		return nil, err
	}
	return ui.AddMissingCategories(ctx, c)
}
