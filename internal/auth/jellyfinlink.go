package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A Jellyfin link is the Plex link's twin: the Jellyfin user's id (from the server Arrmada
// is connected to) on the account, so "Sign in with Jellyfin" finds it, and the name it was
// made with, for "Linked as <name>". Jellyfin passwords are never stored: signing in proves
// who someone is once, and only the id is kept.

// ErrJellyfinAlreadyLinked: the Jellyfin user is linked to another Arrmada account.
type ErrJellyfinAlreadyLinked struct {
	UserID   int64
	Username string
	Role     Role
}

func (e *ErrJellyfinAlreadyLinked) Error() string {
	return fmt.Sprintf("this Jellyfin user is already linked to %s", e.Username)
}

// ErrJellyfinOnlyLogin: the account has no password anyone knows and no Plex link, so
// Jellyfin is its only way in.
var ErrJellyfinOnlyLogin = errors.New("this account signs in only with Jellyfin — set a password for it before unlinking Jellyfin")

// JellyfinLink is an account's Jellyfin link, as its owner sees it.
type JellyfinLink struct {
	Linked           bool   `json:"linked"`
	JellyfinUsername string `json:"jellyfin_username,omitempty"`
	// CanUnlink: the account has a password or a Plex link, so unlinking leaves a way in.
	CanUnlink bool `json:"can_unlink"`
}

// FindOrCreateJellyfinUser returns the account linked to a Jellyfin user, making a
// passwordless one (named after the Jellyfin user, de-duplicated) when there is none. An
// existing account keeps its role and auto-approve; a disabled one is returned as it is so
// the caller can refuse the sign-in.
func (s *Service) FindOrCreateJellyfinUser(ctx context.Context, jellyfinID, jellyfinUsername string, role Role, autoApprove AutoApproval) (*User, error) {
	return s.findOrCreateLinked(ctx, jellyfinLink, jellyfinID, jellyfinUsername, role, autoApprove)
}

// UserByJellyfinID is the account linked to a Jellyfin user id, or nil when there is none.
func (s *Service) UserByJellyfinID(ctx context.Context, jellyfinID string) (*User, error) {
	jellyfinID = strings.TrimSpace(jellyfinID)
	if jellyfinID == "" {
		return nil, nil
	}
	u, err := s.userWhere(ctx, "jellyfin_id = ?", jellyfinID)
	if errors.Is(err, ErrInvalidCredentials) {
		return nil, nil
	}
	return u, err
}

// JellyfinIDForUser is the Jellyfin user id linked to an account, or "".
func (s *Service) JellyfinIDForUser(ctx context.Context, userID int64) string {
	var id *string
	if err := s.db.QueryRowContext(ctx, `SELECT jellyfin_id FROM users WHERE id = ?`, userID).Scan(&id); err != nil || id == nil {
		return ""
	}
	return strings.TrimSpace(*id)
}

// JellyfinLinkFor reports userID's Jellyfin link.
func (s *Service) JellyfinLinkFor(ctx context.Context, userID int64) (JellyfinLink, error) {
	l, err := s.linkFor(ctx, jellyfinLink, userID)
	return JellyfinLink{Linked: l.linked, JellyfinUsername: l.name, CanUnlink: l.canUnlink}, err
}

// LinkJellyfin ties a Jellyfin user to an existing account, replacing any it had. One held
// by another account is refused with *ErrJellyfinAlreadyLinked. Linking grants nothing:
// whether Jellyfin sign-in may open a staff account is the sign-in's decision.
func (s *Service) LinkJellyfin(ctx context.Context, userID int64, jellyfinID, jellyfinUsername string) error {
	h, err := s.link(ctx, jellyfinLink, userID, jellyfinID, jellyfinUsername)
	if h != nil {
		return &ErrJellyfinAlreadyLinked{UserID: h.userID, Username: h.username, Role: h.role}
	}
	return err
}

// UnlinkJellyfin removes an account's Jellyfin link. checkLogin refuses when Jellyfin is
// its last way in (ErrJellyfinOnlyLogin); an admin passes false.
func (s *Service) UnlinkJellyfin(ctx context.Context, userID int64, checkLogin bool) error {
	return s.unlink(ctx, jellyfinLink, userID, checkLogin, ErrJellyfinOnlyLogin)
}
